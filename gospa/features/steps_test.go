// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// bindBuildSteps binds the steps that describe the folder and how it is served.
func bindBuildSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a (built app|public folder) that holds ((?:"[^"]*"(?:, | and )?)+)$`, holds)
	sc.Step(`^a public folder that holds "([^"]*)" that reads:$`, holdsReading)
	sc.Step(`^a built app whose page reads:$`, pageReads)
	sc.Step(`^the build holds no "([^"]*)"$`, holdsNo)
	sc.Step(`^the build writes "([^"]*)"(?: again)? as:$`, buildWrites)
	sc.Step(`^the window for hashed files is (\d+) hours?$`, windowIs)
	sc.Step(`^the files were last changed at "([^"]*)"$`, changedAt)
	sc.Step(`^the files carry no date, as embedded files do$`, carryNoDate)
	sc.Step(`^the app carries the value "([^"]*)" set to "([^"]*)"$`, carriesValue)
	sc.Step(`^the app carries the value "([^"]*)" set to:$`, carriesValueReading)
	sc.Step(`^the caller answers not found in its own way$`, answersNotFound)
}

// bindRequestSteps binds the steps in which a browser asks and the program starts.
func bindRequestSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a browser asks for "([^"]*)"$`, asksFor)
	sc.Step(`^the browser asks for "([^"]*)" again with the (tag|date) it got$`, asksAgain)
	sc.Step(`^a browser asks for "([^"]*)" with the method "([^"]*)"$`, asksWithMethod)
	sc.Step(`^a browser asks for "([^"]*)" with the range "([^"]*)"$`, asksForRange)
	sc.Step(`^the program starts again with the value "([^"]*)" set to "([^"]*)"$`, startsAgain)
	sc.Step(`^the app is set up$`, isSetUp)
}

// bindAnswerSteps binds the steps that check the answer.
func bindAnswerSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the answer is the page$`, isThePage)
	sc.Step(`^the answer is the file "([^"]*)" with the type "([^"]*)"$`, isTheFile)
	sc.Step(`^the answer is not found$`, isNotFound)
	sc.Step(`^the answer reads:$`, reads)
	sc.Step(`^the answer holds:$`, answerHolds)
	sc.Step(`^the answer carries "([^":]+): ([^"]*)"$`, carriesHeader)
	sc.Step(`^the answer carries a tag$`, carriesTag)
	sc.Step(`^the answer carries no date$`, carriesNoDate)
	sc.Step(`^the answer is 304 with no body$`, isNotModified)
	sc.Step(`^the answer is 405 allowing "([^"]*)"$`, isNotAllowed)
	sc.Step(`^the answer is 206 holding the first (\d+) bytes of "([^"]*)"$`, isPart)
	sc.Step(`^the answer carries the length of the page$`, carriesPageLength)
	sc.Step(`^the answer has no body$`, hasNoBody)
	sc.Step(`^setting it up fails naming "([^"]*)"$`, setUpFails)
	sc.Step(`^the caller's own answer is sent$`, isCallersAnswer)
}

// holds puts each name of list in a folder served as source, index.html as a page and the rest named by content.
func holds(ctx context.Context, source, list string) {
	w := worldOf(ctx)
	w.app = source == "built app"
	for _, name := range names(list) {
		data := "content of " + name
		if name == "index.html" {
			data = defaultPage
		}
		w.write(name, data)
	}
}

// holdsReading puts name in a public folder with the doc string as its content.
func holdsReading(ctx context.Context, name string, content *godog.DocString) {
	worldOf(ctx).write(name, content.Content)
}

// pageReads makes the scenario a built app whose page is the doc string.
func pageReads(ctx context.Context, content *godog.DocString) {
	w := worldOf(ctx)
	w.app = true
	w.write("index.html", content.Content)
}

// holdsNo takes name out of the build.
func holdsNo(ctx context.Context, name string) {
	delete(worldOf(ctx).folder, name)
}

// buildWrites puts name in the build with the doc string as its content.
func buildWrites(ctx context.Context, name string, content *godog.DocString) {
	worldOf(ctx).write(name, content.Content)
}

// windowIs sets how many hours a browser keeps a hashed file.
func windowIs(ctx context.Context, hours int) {
	worldOf(ctx).opts.HashedMaxAge = time.Duration(hours) * time.Hour
}

// changedAt makes every file carry the date stamp.
func changedAt(ctx context.Context, stamp string) error {
	changed, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return err
	}
	worldOf(ctx).redate(changed)
	return nil
}

// carryNoDate makes every file carry no date.
func carryNoDate(ctx context.Context) {
	worldOf(ctx).redate(time.Time{})
}

// carriesValue makes the page carry content under name.
func carriesValue(ctx context.Context, name, content string) {
	worldOf(ctx).setValue(name, content)
}

// carriesValueReading makes the page carry the doc string under name.
func carriesValueReading(ctx context.Context, name string, content *godog.DocString) {
	worldOf(ctx).setValue(name, content.Content)
}

// answersNotFound makes the caller answer not found with its own body.
func answersNotFound(ctx context.Context) {
	worldOf(ctx).opts.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(callersAnswer))
	})
}

// asksFor sends a GET for target.
func asksFor(ctx context.Context, target string) error {
	return worldOf(ctx).ask(http.MethodGet, target)
}

// asksAgain sends a GET for target that checks the tag or the date the last answer gave.
func asksAgain(ctx context.Context, target, by string) error {
	w := worldOf(ctx)
	if by == "tag" {
		return w.ask(http.MethodGet, target, "If-None-Match", w.tag)
	}
	return w.ask(http.MethodGet, target, "If-Modified-Since", w.date)
}

// asksWithMethod sends a request for target with method.
func asksWithMethod(ctx context.Context, target, method string) error {
	return worldOf(ctx).ask(method, target)
}

// asksForRange sends a GET for the part of target that span names.
func asksForRange(ctx context.Context, target, span string) error {
	return worldOf(ctx).ask(http.MethodGet, target, "Range", span)
}

// startsAgain sets the value under name to content and sets the app up again.
func startsAgain(ctx context.Context, name, content string) {
	w := worldOf(ctx)
	w.setValue(name, content)
	w.handler = nil
}

// isSetUp sets the app up and keeps the error App gives.
func isSetUp(ctx context.Context) {
	worldOf(ctx).setUp()
}

// isThePage checks that the answer is index.html with every value the app carries written in.
func isThePage(ctx context.Context) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusOK); err != nil {
		return err
	}
	if kind := w.answer.Header().Get("Content-Type"); kind != "text/html; charset=utf-8" {
		return fmt.Errorf("the answer to %s is typed %q, not as a page", w.path, kind)
	}
	body := w.answer.Body.String()
	for _, value := range w.opts.Meta {
		element := `<meta name="` + html.EscapeString(value.Name) + `" content="` + html.EscapeString(value.Content) + `">`
		if strings.Count(body, element) != 1 {
			return fmt.Errorf("the page %q does not hold %q once", body, element)
		}
		body = strings.Replace(body, element, "", 1)
	}
	if page := string(w.folder["index.html"].Data); body != page {
		return fmt.Errorf("the answer to %s reads %q, not the page %q", w.path, w.answer.Body, page)
	}
	return nil
}

// isTheFile checks that the answer sends the file name typed as kind.
func isTheFile(ctx context.Context, name, kind string) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusOK); err != nil {
		return err
	}
	if got := w.answer.Header().Get("Content-Type"); got != kind {
		return fmt.Errorf("the answer to %s is typed %q, not %q", w.path, got, kind)
	}
	if body, want := w.answer.Body.String(), string(w.folder[name].Data); body != want {
		return fmt.Errorf("the answer to %s reads %q, not the file %q", w.path, body, want)
	}
	return nil
}

// isNotFound checks that the answer is 404.
func isNotFound(ctx context.Context) error {
	return hasStatus(worldOf(ctx), http.StatusNotFound)
}

// reads checks that the answer reads the doc string exactly.
func reads(ctx context.Context, content *godog.DocString) error {
	w := worldOf(ctx)
	if body := w.answer.Body.String(); body != content.Content {
		return fmt.Errorf("the answer to %s reads %q, not %q", w.path, body, content.Content)
	}
	return nil
}

// answerHolds checks that the answer holds the doc string.
func answerHolds(ctx context.Context, content *godog.DocString) error {
	w := worldOf(ctx)
	if body := w.answer.Body.String(); !strings.Contains(body, content.Content) {
		return fmt.Errorf("the answer to %s reads %q, which does not hold %q", w.path, body, content.Content)
	}
	return nil
}

// carriesHeader checks that the answer carries value under key.
func carriesHeader(ctx context.Context, key, value string) error {
	w := worldOf(ctx)
	if got := w.answer.Header().Get(key); got != value {
		return fmt.Errorf("the answer to %s carries %s %q, not %q", w.path, key, got, value)
	}
	return nil
}

// carriesTag checks that the answer carries an entity tag.
func carriesTag(ctx context.Context) error {
	w := worldOf(ctx)
	if tag := w.answer.Header().Get("ETag"); !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) {
		return fmt.Errorf("the answer to %s carries the tag %q, not a strong tag", w.path, tag)
	}
	return nil
}

// carriesNoDate checks that the answer carries no Last-Modified date.
func carriesNoDate(ctx context.Context) error {
	w := worldOf(ctx)
	if dates := w.answer.Header().Values("Last-Modified"); len(dates) > 0 {
		return fmt.Errorf("the answer to %s carries the date %q", w.path, dates)
	}
	return nil
}

// isNotModified checks that the answer is 304 with no body.
func isNotModified(ctx context.Context) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusNotModified); err != nil {
		return err
	}
	return hasNoBody(ctx)
}

// isNotAllowed checks that the answer is 405 and allows methods.
func isNotAllowed(ctx context.Context, methods string) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusMethodNotAllowed); err != nil {
		return err
	}
	return carriesHeader(ctx, "Allow", methods)
}

// isPart checks that the answer is 206 holding the first count bytes of name.
func isPart(ctx context.Context, count int, name string) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusPartialContent); err != nil {
		return err
	}
	if body, want := w.answer.Body.String(), string(w.folder[name].Data[:count]); body != want {
		return fmt.Errorf("the answer to %s holds %q, not %q", w.path, body, want)
	}
	return nil
}

// carriesPageLength checks that the answer carries the length of the page a GET of the same path sends.
func carriesPageLength(ctx context.Context) error {
	w := worldOf(ctx)
	head := w.answer
	if err := w.ask(http.MethodGet, w.path); err != nil {
		return err
	}
	length, want := head.Header().Get("Content-Length"), strconv.Itoa(w.answer.Body.Len())
	w.answer = head
	if length != want {
		return fmt.Errorf("the answer to HEAD %s carries the length %q, not %s", w.path, length, want)
	}
	return nil
}

// hasNoBody checks that the answer has no body.
func hasNoBody(ctx context.Context) error {
	w := worldOf(ctx)
	if w.answer.Body.Len() != 0 {
		return fmt.Errorf("the answer to %s has the body %q", w.path, w.answer.Body)
	}
	return nil
}

// setUpFails checks that App failed with an error naming name.
func setUpFails(ctx context.Context, name string) error {
	w := worldOf(ctx)
	if w.setupErr == nil {
		return errors.New("the app was set up")
	}
	if !strings.Contains(w.setupErr.Error(), name) {
		return fmt.Errorf("setting the app up failed with %q, which does not name %q", w.setupErr, name)
	}
	return nil
}

// isCallersAnswer checks that the answer is the caller's own not found answer.
func isCallersAnswer(ctx context.Context) error {
	w := worldOf(ctx)
	if err := hasStatus(w, http.StatusNotFound); err != nil {
		return err
	}
	if body := w.answer.Body.String(); body != callersAnswer {
		return fmt.Errorf("the answer to %s reads %q, not the caller's own answer", w.path, body)
	}
	return nil
}

// hasStatus checks that the answer has the status code.
func hasStatus(w *world, code int) error {
	if w.answer == nil {
		return errors.New("no browser asked for anything")
	}
	if w.answer.Code != code {
		return fmt.Errorf("the answer to %s is %d %q, not %d", w.path, w.answer.Code, w.answer.Body, code)
	}
	return nil
}
