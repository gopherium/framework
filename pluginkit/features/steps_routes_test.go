// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit"
)

// clientTimeout bounds how long a scenario waits for a client or a handler.
const clientTimeout = 10 * time.Second

// action is one thing a scenario's handler does while it serves a request.
type action func(w *routeWorld, h *handling)

// handling is what one request's handler holds between the things it does.
type handling struct {
	writer http.ResponseWriter
	conn   net.Conn
}

// servedPlugin is one plugin a routes scenario mounts and what its handler does.
type servedPlugin struct {
	id        string
	path      string
	public    bool
	noHandler bool
	actions   []action
}

// answered is what a client got for one request.
type answered struct {
	status  int
	proto   int
	header  http.Header
	body    string
	err     error
	bodyErr error
}

// namedPlugin is a plugin whose routes a scenario reads.
type namedPlugin interface{ ID() string }

// plainPlugin is a registered plugin that offers neither routes nor public paths.
type plainPlugin struct{ id string }

// ID returns the plugin's id.
func (p plainPlugin) ID() string {
	return p.id
}

// routedPlugin is a registered plugin that offers routes.
type routedPlugin struct {
	plainPlugin
	routes func() http.Handler
}

// Routes returns what the scenario has the plugin answer.
func (p routedPlugin) Routes() http.Handler {
	return p.routes()
}

// publicPlugin is a registered plugin that offers routes and public paths.
type publicPlugin struct {
	routedPlugin
	paths func() []string
}

// PublicPaths returns what the scenario has the plugin answer.
func (p publicPlugin) PublicPaths() []string {
	return p.paths()
}

// routeWorld is what one scenario of the routes feature serves, reads and finds.
type routeWorld struct {
	http2           bool
	plugins         []servedPlugin
	middlewarePanic string
	middlewareRan   atomic.Bool
	server          *httptest.Server
	client          *http.Client
	served          sync.WaitGroup
	mu              sync.Mutex
	failures        []pluginkit.RouteFailure
	calls           []error
	last            answered
	failure         pluginkit.RouteFailure
	protectPanic    any
	registered      []string
	hasRoutes       map[string]bool
	publicPath      map[string]string
	routesPanic     map[string]string
	pathsPanic      map[string]string
	noHandler       map[string]bool
	outcome         pluginkit.RoutesOutcome
}

// panicWith panics with value, so the stack of a scenario's panic names it.
func panicWith(value any) {
	panic(value)
}

// handlerActions lists each thing a scenario's handler can do and builds it from the words that name it.
var handlerActions = []struct {
	pattern *regexp.Regexp
	build   func(match []string) action
}{
	{regexp.MustCompile(`^answers status (\d+) and "([^"]*)"$`), answersStatus},
	{regexp.MustCompile(`^answers status (\d+) and "([^"]*)" on the connection$`), answersOnConnection},
	{regexp.MustCompile(`^panics with "([^"]*)"$`), panics},
	{regexp.MustCompile(`^aborts$`), aborts},
	{regexp.MustCompile(`^writes the status (\d+)$`), writesStatus},
	{regexp.MustCompile(`^writes the body "([^"]*)"$`), writesBody},
	{regexp.MustCompile(`^flushes the body "([^"]*)"$`), flushesBody},
	{regexp.MustCompile(`^hijacks the connection( through http\.Hijacker)?$`), hijacks},
	{regexp.MustCompile(`^sends 103 Early Hints$`), sendsEarlyHints},
	{regexp.MustCompile(`^sets the header "([^"]*)" and a cookie$`), setsHeaderAndCookie},
	{regexp.MustCompile(`^flushes through http\.Flusher$`), flushesThroughFlusher},
	{regexp.MustCompile(`^(flushes|sets a read deadline|enables full duplex) through a response controller$`), controls},
}

// actionOf returns the action the words name.
func actionOf(words string) (action, error) {
	for _, known := range handlerActions {
		if match := known.pattern.FindStringSubmatch(words); match != nil {
			return known.build(match), nil
		}
	}
	return nil, fmt.Errorf("no handler action reads %q", words)
}

// answersStatus writes the status and the body.
func answersStatus(match []string) action {
	status, _ := strconv.Atoi(match[1])
	return func(_ *routeWorld, h *handling) {
		h.writer.WriteHeader(status)
		_, _ = io.WriteString(h.writer, match[2])
	}
}

// answersOnConnection writes a whole answer with the status and the body on the hijacked connection, then closes it.
func answersOnConnection(match []string) action {
	status, _ := strconv.Atoi(match[1])
	return func(w *routeWorld, h *handling) {
		if h.conn == nil {
			w.called(errors.New("no connection was hijacked"))
			return
		}
		answer := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			status, http.StatusText(status), len(match[2]), match[2])
		_, err := io.WriteString(h.conn, answer)
		w.called(errors.Join(err, h.conn.Close()))
	}
}

// panics panics with the text.
func panics(match []string) action {
	return func(*routeWorld, *handling) { panicWith(match[1]) }
}

// aborts panics with the value that tells the server to cut the answer.
func aborts([]string) action {
	return func(*routeWorld, *handling) { panicWith(http.ErrAbortHandler) }
}

// writesStatus writes the status alone.
func writesStatus(match []string) action {
	status, _ := strconv.Atoi(match[1])
	return func(_ *routeWorld, h *handling) { h.writer.WriteHeader(status) }
}

// writesBody writes the body without flushing it.
func writesBody(match []string) action {
	return func(_ *routeWorld, h *handling) { _, _ = io.WriteString(h.writer, match[1]) }
}

// flushesBody writes the body and flushes it to the client.
func flushesBody(match []string) action {
	return func(w *routeWorld, h *handling) {
		_, _ = io.WriteString(h.writer, match[1])
		w.called(http.NewResponseController(h.writer).Flush())
	}
}

// hijacks takes over the connection through a response controller, or through http.Hijacker when the words say so.
func hijacks(match []string) action {
	return func(w *routeWorld, h *handling) {
		take := http.NewResponseController(h.writer).Hijack
		if match[1] != "" {
			hijacker, ok := h.writer.(http.Hijacker)
			if !ok {
				w.called(errors.New("the writer is not an http.Hijacker"))
				return
			}
			take = hijacker.Hijack
		}
		conn, _, err := take()
		h.conn = conn
		w.called(err)
	}
}

// sendsEarlyHints sends an informational 103 with a Link header.
func sendsEarlyHints([]string) action {
	return func(_ *routeWorld, h *handling) {
		h.writer.Header().Set("Link", "</feed.css>; rel=preload; as=style")
		h.writer.WriteHeader(http.StatusEarlyHints)
	}
}

// setsHeaderAndCookie sets the header and a cookie without writing them.
func setsHeaderAndCookie(match []string) action {
	return func(_ *routeWorld, h *handling) {
		h.writer.Header().Set(match[1], "plugin")
		http.SetCookie(h.writer, &http.Cookie{Name: "feed", Value: "plugin"})
	}
}

// flushesThroughFlusher flushes through the writer's own http.Flusher.
func flushesThroughFlusher([]string) action {
	return func(w *routeWorld, h *handling) {
		flusher, ok := h.writer.(http.Flusher)
		if !ok {
			w.called(errors.New("the writer is not an http.Flusher"))
			return
		}
		flusher.Flush()
		w.called(nil)
	}
}

// controls flushes, sets a read deadline or enables full duplex through a response controller.
func controls(match []string) action {
	return func(w *routeWorld, h *handling) {
		controller := http.NewResponseController(h.writer)
		switch match[1] {
		case "flushes":
			w.called(controller.Flush())
		case "sets a read deadline":
			w.called(controller.SetReadDeadline(time.Now().Add(time.Minute)))
		default:
			w.called(controller.EnableFullDuplex())
		}
	}
}

// called keeps the error a call the handler made on its writer answered.
func (w *routeWorld) called(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, err)
}

// report is the scenario's report, keeping each failure.
func (w *routeWorld) report(failure pluginkit.RouteFailure) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failures = append(w.failures, failure)
}

// applicationAnswer is the scenarios' answer to a request whose plugin handler panicked.
func applicationAnswer(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, "internal error")
}

// middleware is the scenario's middleware, which notes that it ran and sets a header of its own.
func (w *routeWorld) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		w.middlewareRan.Store(true)
		writer.Header().Set("X-Middleware", "ran")
		if w.middlewarePanic != "" {
			panicWith(w.middlewarePanic)
		}
		next.ServeHTTP(writer, r)
	})
}

// settings are the route settings every plugin of the scenario is protected under.
func (w *routeWorld) settings() pluginkit.RouteSettings {
	return pluginkit.RouteSettings{Wrap: w.middleware, Answer: http.HandlerFunc(applicationAnswer), Report: w.report}
}

// serves mounts the plugin id on path with a handler that does what the words say, in order.
func (w *routeWorld) serves(id, public, path, words string) error {
	plugin := servedPlugin{id: id, path: path, public: public != ""}
	for _, part := range strings.Split(words, ", then ") {
		act, err := actionOf(part)
		if err != nil {
			return err
		}
		plugin.actions = append(plugin.actions, act)
	}
	w.plugins = append(w.plugins, plugin)
	return nil
}

// handlerOf returns the handler of the plugin, nil when it serves none.
func (w *routeWorld) handlerOf(plugin servedPlugin) http.Handler {
	if plugin.noHandler {
		return nil
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		h := &handling{writer: writer}
		for _, act := range plugin.actions {
			act(w, h)
		}
	})
}

// counted serves next, counting the request until next returns or panics.
func (w *routeWorld) counted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		w.served.Add(1)
		defer w.served.Done()
		next.ServeHTTP(writer, r)
	})
}

// start mounts each plugin under its id and starts the server, over HTTP/2 when the scenario asks.
func (w *routeWorld) start() {
	mux := http.NewServeMux()
	for _, plugin := range w.plugins {
		var public []string
		if plugin.public {
			public = []string{plugin.path}
		}
		protected := pluginkit.Protect(plugin.id, w.handlerOf(plugin), public, w.settings())
		mux.Handle("/"+plugin.id+"/", http.StripPrefix("/"+plugin.id, w.counted(protected)))
	}
	w.server = httptest.NewUnstartedServer(mux)
	w.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	if w.http2 {
		w.server.EnableHTTP2 = true
		w.server.StartTLS()
		w.client = w.server.Client()
		w.client.Timeout = clientTimeout
		return
	}
	w.server.Start()
	w.client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: clientTimeout}
}

// ask has a client ask the plugin id for path, on a connection of its own.
func (w *routeWorld) ask(id, path string) answered {
	if w.server == nil {
		w.start()
	}
	response, err := w.client.Get(w.server.URL + "/" + id + path)
	if err != nil {
		return answered{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	return answered{
		status: response.StatusCode, proto: response.ProtoMajor, header: response.Header, body: string(body), bodyErr: err,
	}
}

// stop stops the scenario's server once the scenario ends.
func (w *routeWorld) stop(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if w.server != nil {
		w.client.CloseIdleConnections()
		w.server.Close()
	}
	return ctx, nil
}

// settled waits until every request the server took has left its handler.
func (w *routeWorld) settled() error {
	done := make(chan struct{})
	go func() {
		w.served.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(clientTimeout):
		return errors.New("a handler is still running")
	}
}

// timedOut reports whether err is a timeout, which a cut answer never is.
func timedOut(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// is checks that the answer came whole with the status and the body.
func (a answered) is(status int, body string) error {
	if err := errors.Join(a.err, a.bodyErr); err != nil {
		return fmt.Errorf("the client got no whole answer: %w", err)
	}
	if a.status != status || a.body != body {
		return fmt.Errorf("the client got %d %q, want %d %q", a.status, a.body, status, body)
	}
	return nil
}

// clientGets checks the last answer.
func (w *routeWorld) clientGets(status int, body string) error {
	return w.last.is(status, body)
}

// askingGets has a client ask the plugin id for path and checks its answer.
func (w *routeWorld) askingGets(id, path string, status int, body string) error {
	return w.ask(id, path).is(status, body)
}

// answerCut checks that the last request failed, or that its 200 answer broke off, and neither for a timeout.
func (w *routeWorld) answerCut() error {
	a := w.last
	if a.err != nil && !timedOut(a.err) {
		return nil
	}
	if a.err == nil && a.status == http.StatusOK && a.bodyErr != nil && !timedOut(a.bodyErr) {
		return nil
	}
	return fmt.Errorf("the client got %d %q with %v, want the answer cut", a.status, a.body, errors.Join(a.err, a.bodyErr))
}

// reportHoldsOne checks that the report holds exactly one failure, of the plugin id for the request, and keeps it.
func (w *routeWorld) reportHoldsOne(id, method, path, how string) error {
	if err := w.settled(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.failures) != 1 {
		return fmt.Errorf("the report holds %d failures, want 1: %+v", len(w.failures), w.failures)
	}
	got, answeredWanted := w.failures[0], how == "answered"
	if got.ID != id || got.Method != method || got.Path != path || got.Answered != answeredWanted {
		return fmt.Errorf("the report holds %s %s %q answered %t, want %s %s %q answered %t",
			got.ID, got.Method, got.Path, got.Answered, id, method, path, answeredWanted)
	}
	w.failure = got
	return nil
}

// failureReads checks the text of the kept failure and that its stack holds the panic.
func (w *routeWorld) failureReads(text string) error {
	if w.failure.Err == nil || w.failure.Err.Error() != text {
		return fmt.Errorf("the failure reads %v, want %q", w.failure.Err, text)
	}
	if !strings.Contains(w.failure.Stack, "panicWith") {
		return fmt.Errorf("the stack does not hold the panic: %s", w.failure.Stack)
	}
	return nil
}

// reportHoldsNothing checks that no failure was reported.
func (w *routeWorld) reportHoldsNothing() error {
	if err := w.settled(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.failures) != 0 {
		return fmt.Errorf("the report holds %+v, want nothing", w.failures)
	}
	return nil
}

// middlewareRanAs checks whether the middleware ran.
func (w *routeWorld) middlewareRanAs(how string) error {
	if ran := w.middlewareRan.Load(); ran != (how == "ran") {
		return fmt.Errorf("the middleware ran: %t", ran)
	}
	return nil
}

// noHeaderNorCookie checks that the last answer holds neither the header nor a cookie.
func (w *routeWorld) noHeaderNorCookie(name string) error {
	if got, cookies := w.last.header.Get(name), w.last.header.Values("Set-Cookie"); got != "" || len(cookies) > 0 {
		return fmt.Errorf("the answer holds %s %q and the cookies %q", name, got, cookies)
	}
	return nil
}

// middlewareHeader checks that the last answer holds the header the middleware set.
func (w *routeWorld) middlewareHeader() error {
	if got := w.last.header.Get("X-Middleware"); got != "ran" {
		return fmt.Errorf("the answer holds X-Middleware %q, want %q", got, "ran")
	}
	return nil
}

// overHTTP2 checks that the last answer came over HTTP/2.
func (w *routeWorld) overHTTP2() error {
	if w.last.proto != 2 {
		return fmt.Errorf("the answer came over HTTP/%d", w.last.proto)
	}
	return nil
}

// callsSucceeded checks that the handler made a call on its writer and that every one succeeded.
func (w *routeWorld) callsSucceeded() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) == 0 {
		return errors.New("the handler made no call on its writer")
	}
	return errors.Join(w.calls...)
}

// protectWith protects a plugin's routes with the setting the words name left out, keeping what Protect panicked with.
func (w *routeWorld) protectWith(missing string) {
	id, settings := "feed", w.settings()
	switch missing {
	case "no plugin id":
		id = ""
	case "no middleware":
		settings.Wrap = nil
	case "no answer":
		settings.Answer = nil
	default:
		settings.Report = nil
	}
	defer func() { w.protectPanic = recover() }()
	pluginkit.Protect(id, http.NotFoundHandler(), nil, settings)
}

// protectPanicked checks what Protect panicked with.
func (w *routeWorld) protectPanicked(text string) error {
	if w.protectPanic == nil || fmt.Sprint(w.protectPanic) != text {
		return fmt.Errorf("Protect panicked with %v, want %q", w.protectPanic, text)
	}
	return nil
}

// hasRoutesWith gives the plugin id routes, and the public path when the words name one.
func (w *routeWorld) hasRoutesWith(id, path string) {
	w.hasRoutes[id] = true
	if path != "" {
		w.publicPath[id] = path
	}
}

// panicsOnRead makes the plugin id's Routes or PublicPaths panic with the text.
func (w *routeWorld) panicsOnRead(method, id, text string) {
	if method == "Routes" {
		w.routesPanic[id] = text
		return
	}
	w.pathsPanic[id] = text
}

// registeredPlugin returns the plugin id offering what the scenario gave it.
func (w *routeWorld) registeredPlugin(id string) namedPlugin {
	plain := plainPlugin{id: id}
	if !w.hasRoutes[id] {
		return plain
	}
	routed := routedPlugin{plainPlugin: plain, routes: func() http.Handler {
		if text := w.routesPanic[id]; text != "" {
			panicWith(text)
		}
		if w.noHandler[id] {
			return nil
		}
		return http.NotFoundHandler()
	}}
	path, public := w.publicPath[id]
	if !public {
		return routed
	}
	return publicPlugin{routedPlugin: routed, paths: func() []string {
		if text := w.pathsPanic[id]; text != "" {
			panicWith(text)
		}
		return []string{path}
	}}
}

// readRoutes reads the routes of each registered plugin.
func (w *routeWorld) readRoutes() {
	plugins := make([]namedPlugin, 0, len(w.registered))
	for _, id := range w.registered {
		plugins = append(plugins, w.registeredPlugin(id))
	}
	w.outcome = pluginkit.RoutesEach(plugins)
}

// routesHold checks that the outcome holds a handler for exactly the listed plugins.
func (w *routeWorld) routesHold(list string) error {
	got := slices.Sorted(maps.Keys(w.outcome.Routes))
	if want := slices.Sorted(slices.Values(names(list))); !slices.Equal(got, want) {
		return fmt.Errorf("the routes hold %q, want %q", got, want)
	}
	for id, handler := range w.outcome.Routes {
		if handler == nil {
			return fmt.Errorf("the routes hold no handler for %q", id)
		}
	}
	return nil
}

// publicPathsHold checks that the outcome holds the public path for the plugin id and nothing else.
func (w *routeWorld) publicPathsHold(path, id string) error {
	if want := map[string][]string{id: {path}}; !maps.EqualFunc(w.outcome.PublicPaths, want, slices.Equal) {
		return fmt.Errorf("the public paths hold %q, want %q", w.outcome.PublicPaths, want)
	}
	return nil
}

// noneFailed checks that no plugin failed to load its routes.
func (w *routeWorld) noneFailed() error {
	if len(w.outcome.Failed) != 0 {
		return fmt.Errorf("failed %+v, want none", w.outcome.Failed)
	}
	return nil
}

// failedAt checks that the plugin id is the one failure, at the step, with the text, and whether it panicked.
func (w *routeWorld) failedAt(id, step, text, how string) error {
	if len(w.outcome.Failed) != 1 {
		return fmt.Errorf("failed %+v, want one failure", w.outcome.Failed)
	}
	got := w.outcome.Failed[0]
	if got.ID != id || string(got.Step) != step || got.Err == nil || got.Err.Error() != text ||
		got.Panicked != (how == "as a panic") {
		return fmt.Errorf("failed %+v, want %s at %s with %q %s", got, id, step, text, how)
	}
	return nil
}

// givenRoutes registers the steps that set up what a scenario serves or reads.
func givenRoutes(sc *godog.ScenarioContext, w *routeWorld) {
	sc.Step(`^the plugin "([^"]*)" serves (the public path )?"([^"]*)" with a handler that (.+)$`, w.serves)
	sc.Step(`^the plugin "([^"]*)" serves no handler$`,
		func(id string) { w.plugins = append(w.plugins, servedPlugin{id: id, noHandler: true}) })
	sc.Step(`^the middleware panics with "([^"]*)"$`, func(text string) { w.middlewarePanic = text })
	sc.Step(`^the server speaks HTTP/2$`, func() { w.http2 = true })
	sc.Step(`^the registered plugins (.+)$`, func(list string) { w.registered = names(list) })
	sc.Step(`^"([^"]*)" has routes(?: and the public path "([^"]*)")?$`, w.hasRoutesWith)
	sc.Step(`^the (Routes|PublicPaths) of "([^"]*)" panics with "([^"]*)"$`, w.panicsOnRead)
	sc.Step(`^the Routes of "([^"]*)" answers no handler$`, func(id string) { w.noHandler[id] = true })
}

// whenRoutes registers the steps that serve or read a scenario's routes.
func whenRoutes(sc *godog.ScenarioContext, w *routeWorld) {
	sc.Step(`^a client asks "([^"]*)" for "([^"]*)"$`, func(id, path string) { w.last = w.ask(id, path) })
	sc.Step(`^the application protects a plugin's routes with (no plugin id|no middleware|no answer|no report)$`,
		w.protectWith)
	sc.Step(`^the application reads the routes of each plugin$`, w.readRoutes)
}

// thenServing registers the steps that check what a scenario's server answered and reported.
func thenServing(sc *godog.ScenarioContext, w *routeWorld) {
	sc.Step(`^the client gets status (\d+) and "([^"]*)"$`, w.clientGets)
	sc.Step(`^a client asking "([^"]*)" for "([^"]*)" gets status (\d+) and "([^"]*)"$`, w.askingGets)
	sc.Step(`^the client's answer is cut$`, w.answerCut)
	sc.Step(`^the report holds one failure of "([^"]*)" for (\S+) "([^"]*)" `+
		`that the application (answered|did not answer)$`, w.reportHoldsOne)
	sc.Step(`^that failure reads "([^"]*)" with the stack of the panic$`, w.failureReads)
	sc.Step(`^the report holds nothing$`, w.reportHoldsNothing)
	sc.Step(`^the middleware (ran|did not run)$`, w.middlewareRanAs)
	sc.Step(`^the answer holds no header "([^"]*)" and no cookie$`, w.noHeaderNorCookie)
	sc.Step(`^the answer holds the header the middleware set$`, w.middlewareHeader)
	sc.Step(`^the answer came over HTTP/2$`, w.overHTTP2)
	sc.Step(`^every call the handler made on its writer succeeded$`, w.callsSucceeded)
	sc.Step(`^the protect panics with "([^"]*)"$`, w.protectPanicked)
}

// thenReading registers the steps that check the routes a scenario read.
func thenReading(sc *godog.ScenarioContext, w *routeWorld) {
	sc.Step(`^the routes hold ((?:"[^"]*"(?:, | and )?)+)(?: only)?$`, w.routesHold)
	sc.Step(`^the public paths hold "([^"]*)" for "([^"]*)" only$`, w.publicPathsHold)
	sc.Step(`^no plugin failed to load its routes$`, w.noneFailed)
	sc.Step(`^"([^"]*)" failed at the step "([^"]*)" with "([^"]*)" (as a panic|not as a panic)$`, w.failedAt)
}

// initializeRoutes gives each routes scenario a world of its own and its steps.
func initializeRoutes() func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &routeWorld{
			hasRoutes: map[string]bool{}, publicPath: map[string]string{}, routesPanic: map[string]string{},
			pathsPanic: map[string]string{}, noHandler: map[string]bool{},
		}
		sc.After(w.stop)
		givenRoutes(sc, w)
		whenRoutes(sc, w)
		thenServing(sc, w)
		thenReading(sc, w)
	}
}
