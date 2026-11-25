// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing/fstest"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/gospa"
)

// built is the date every file of a scenario was last changed, unless the scenario says otherwise.
var built = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// defaultPage is the page a built app holds when a scenario names index.html without its content.
const defaultPage = `<!doctype html><html><head><title>App</title></head><body><div id="root"></div></body></html>`

// callersAnswer is the body of the caller's own not found answer.
const callersAnswer = "the caller's own not found answer"

// quoted matches each quoted name of a list such as "a", "b" and "c".
var quoted = regexp.MustCompile(`"([^"]*)"`)

// world is the state of one scenario.
type world struct {
	app      bool
	folder   fstest.MapFS
	changed  time.Time
	opts     gospa.AppOptions
	handler  http.Handler
	setupErr error
	path     string
	answer   *httptest.ResponseRecorder
	tag      string
	date     string
}

// worldKey carries a scenario's world in its context.
type worldKey struct{}

// worldOf returns the world of the scenario ctx belongs to.
func worldOf(ctx context.Context) *world {
	return ctx.Value(worldKey{}).(*world)
}

// initializeScenario gives each scenario a fresh world and binds every step.
func initializeScenario(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return context.WithValue(ctx, worldKey{}, &world{folder: fstest.MapFS{}, changed: built}), nil
	})
	bindBuildSteps(sc)
	bindRequestSteps(sc)
	bindAnswerSteps(sc)
}

// names returns the quoted names of list in order.
func names(list string) []string {
	var found []string
	for _, match := range quoted.FindAllStringSubmatch(list, -1) {
		found = append(found, match[1])
	}
	return found
}

// write puts name in the folder with data as its content, changed when the scenario's files were.
func (w *world) write(name, data string) {
	w.folder[name] = &fstest.MapFile{Data: []byte(data), ModTime: w.changed}
}

// redate makes every file of the folder carry changed as its date.
func (w *world) redate(changed time.Time) {
	w.changed = changed
	for _, file := range w.folder {
		file.ModTime = changed
	}
}

// setValue sets the value the app carries under name, keeping its place when it carries it already.
func (w *world) setValue(name, content string) {
	for i, value := range w.opts.Meta {
		if value.Name == name {
			w.opts.Meta[i].Content = content
			return
		}
	}
	w.opts.Meta = append(w.opts.Meta, gospa.Meta{Name: name, Content: content})
}

// setUp builds the handler of the scenario's folder, keeping the error when App fails.
func (w *world) setUp() {
	if !w.app {
		w.handler = gospa.Files(w.folder, gospa.FilesOptions{NotFound: w.opts.NotFound})
		return
	}
	w.handler, w.setupErr = gospa.App(w.folder, w.opts)
}

// ask sends one request for target with the given header pairs and keeps the answer, its tag and its date.
func (w *world) ask(method, target string, header ...string) error {
	if w.handler == nil {
		w.setUp()
	}
	if w.setupErr != nil {
		return fmt.Errorf("the app was not set up: %w", w.setupErr)
	}
	request := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	w.path = target
	w.answer = httptest.NewRecorder()
	w.handler.ServeHTTP(w.answer, request)
	if tag := w.answer.Header().Get("ETag"); tag != "" {
		w.tag = tag
	}
	if date := w.answer.Header().Get("Last-Modified"); date != "" {
		w.date = date
	}
	return nil
}
