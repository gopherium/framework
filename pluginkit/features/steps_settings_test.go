// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit/settings"
	"github.com/gopherium/framework/pluginkit/wire"
)

// settingsWorld is what one scenario of the settings features declares, finds, saves and works out.
type settingsWorld struct {
	rules    settings.Rules
	declared map[string][]wire.Setting
	found    map[string]string
	saved    map[string]string
	schema   settings.Schema
	values   settings.Values
	rejected []settings.RejectedSavedValue
	err      error
}

// declaration reads the words of one declaration step as the settings they declare.
type declaration struct {
	pattern *regexp.Regexp
	build   func(match []string) ([]wire.Setting, error)
}

// declarations lists each declaration a scenario can make, in the words that name it.
var declarations = []declaration{
	{regexp.MustCompile(`^a setting "([^"]*)" of the type "([^"]*)"$`), func(m []string) ([]wire.Setting, error) {
		return []wire.Setting{{Key: m[1], Type: m[2]}}, nil
	}},
	{regexp.MustCompile(`^(?:a|the) text setting "([^"]*)"$`), func(m []string) ([]wire.Setting, error) {
		return []wire.Setting{{Key: m[1], Type: settings.TypeText}}, nil
	}},
	{regexp.MustCompile(`^the text setting "([^"]*)" twice$`), func(m []string) ([]wire.Setting, error) {
		return []wire.Setting{{Key: m[1], Type: settings.TypeText}, {Key: m[1], Type: settings.TypeText}}, nil
	}},
	{regexp.MustCompile(`^a (\w+) "([^"]*)" with no (?:default|options)$`), func(m []string) ([]wire.Setting, error) {
		return []wire.Setting{{Key: m[2], Type: m[1]}}, nil
	}},
	{regexp.MustCompile(`^a (\w+) "([^"]*)" with the default "([^"]*)"$`), func(m []string) ([]wire.Setting, error) {
		return []wire.Setting{{Key: m[2], Type: m[1], Default: m[3]}}, nil
	}},
	{regexp.MustCompile(`^a count "([^"]*)" from (\S+) to (\S+) with the default "([^"]*)"$`), countFromTo},
	{regexp.MustCompile(`^a count "([^"]*)" from (\S+) with the default "([^"]*)"$`), countFrom},
	{regexp.MustCompile(`^a (\w+) "([^"]*)" with the (min|max) (\S+)$`), withBound},
	{regexp.MustCompile(`^a (\w+) "([^"]*)" with the options "([^"]*)" and "([^"]*)"$`),
		func(m []string) ([]wire.Setting, error) {
			return []wire.Setting{{Key: m[2], Type: m[1], Options: []string{m[3], m[4]}}}, nil
		}},
	{regexp.MustCompile(`^a choice "([^"]*)" of "([^"]*)" and "([^"]*)" with no default$`),
		func(m []string) ([]wire.Setting, error) {
			return []wire.Setting{{Key: m[1], Type: settings.TypeChoice, Options: []string{m[2], m[3]}}}, nil
		}},
	{regexp.MustCompile(`^a choice "([^"]*)" of "([^"]*)" and "([^"]*)" with the default "([^"]*)"$`),
		func(m []string) ([]wire.Setting, error) {
			return []wire.Setting{{Key: m[1], Type: settings.TypeChoice, Options: []string{m[2], m[3]}, Default: m[4]}}, nil
		}},
	{regexp.MustCompile(`^a (\w+) "([^"]*)" that names the content type "([^"]*)"$`),
		func(m []string) ([]wire.Setting, error) {
			return []wire.Setting{{Key: m[2], Type: m[1], Of: m[3]}}, nil
		}},
}

// boundOf reads text as the min or max of a declared setting, nil when text is empty.
func boundOf(text string) (*float64, error) {
	if text == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, fmt.Errorf("the bound %q is not a number", text)
	}
	return &n, nil
}

// countFromTo declares a count with a min, a max and a default.
func countFromTo(m []string) ([]wire.Setting, error) {
	low, lowErr := boundOf(m[2])
	high, highErr := boundOf(m[3])
	if err := errors.Join(lowErr, highErr); err != nil {
		return nil, err
	}
	return []wire.Setting{{Key: m[1], Type: settings.TypeCount, Min: low, Max: high, Default: m[4]}}, nil
}

// countFrom declares a count with a min and a default.
func countFrom(m []string) ([]wire.Setting, error) {
	low, err := boundOf(m[2])
	if err != nil {
		return nil, err
	}
	return []wire.Setting{{Key: m[1], Type: settings.TypeCount, Min: low, Default: m[3]}}, nil
}

// withBound declares a setting of any type with a min or a max.
func withBound(m []string) ([]wire.Setting, error) {
	n, err := boundOf(m[4])
	if err != nil {
		return nil, err
	}
	setting := wire.Setting{Key: m[2], Type: m[1], Max: n}
	if m[3] == "min" {
		setting.Min, setting.Max = n, nil
	}
	return []wire.Setting{setting}, nil
}

// optionsOf reads a comma list of options, nil when text is empty.
func optionsOf(text string) []string {
	if text == "" {
		return nil
	}
	options := strings.Split(text, ",")
	for i, option := range options {
		options[i] = strings.TrimSpace(option)
	}
	return options
}

// columnsOf maps each heading of table to its column.
func columnsOf(table *godog.Table) map[string]int {
	columns := map[string]int{}
	for i, cell := range table.Rows[0].Cells {
		columns[cell.Value] = i
	}
	return columns
}

// settingOf reads the cells of one row of a settings table under columns, the label and help only under their headings.
func settingOf(cells []string, columns map[string]int) (wire.Setting, error) {
	cell := func(name string) string { return cells[columns[name]] }
	low, lowErr := boundOf(cell("min"))
	high, highErr := boundOf(cell("max"))
	setting := wire.Setting{
		Key: cell("key"), Type: cell("type"), Default: cell("default"),
		Label: optionalCell(cells, columns, "label"), Help: optionalCell(cells, columns, "help"),
		Min: low, Max: high, Options: optionsOf(cell("options")),
	}
	return setting, errors.Join(lowErr, highErr)
}

// optionalCell returns the cell under the heading name, empty when the table has no such heading.
func optionalCell(cells []string, columns map[string]int, name string) string {
	i, ok := columns[name]
	if !ok {
		return ""
	}
	return cells[i]
}

// namesUnder sets the prefix of every variable.
func (w *settingsWorld) namesUnder(prefix string) {
	w.rules.Prefix = prefix
}

// reserves sets the two key starts the application reserves.
func (w *settingsWorld) reserves(first, second string) {
	w.rules.Reserved = []string{first, second}
}

// declaresTable has plugin id declare each setting of table.
func (w *settingsWorld) declaresTable(id string, table *godog.Table) error {
	columns := columnsOf(table)
	declared := make([]wire.Setting, 0, len(table.Rows)-1)
	for _, row := range table.Rows[1:] {
		cells := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			cells = append(cells, cell.Value)
		}
		setting, err := settingOf(cells, columns)
		if err != nil {
			return err
		}
		declared = append(declared, setting)
	}
	w.declared[id] = declared
	return nil
}

// declaresNone has plugin id declare no settings.
func (w *settingsWorld) declaresNone(id string) {
	w.declared[id] = nil
}

// declares has plugin id declare the settings the words name.
func (w *settingsWorld) declares(id, words string) error {
	for _, d := range declarations {
		if match := d.pattern.FindStringSubmatch(words); match != nil {
			declared, err := d.build(match)
			w.declared[id] = declared
			return err
		}
	}
	return fmt.Errorf("no declaration reads %q", words)
}

// holds puts value in the environment the application found, under variable.
func (w *settingsWorld) holds(value, variable string) {
	w.found[variable] = value
}

// savedAs puts value among the values an administrator saved, under key.
func (w *settingsWorld) savedAs(key, value string) {
	w.saved[key] = value
}

// declaredBy returns the settings plugin id declared, an error when the scenario declared none for it.
func (w *settingsWorld) declaredBy(id string) ([]wire.Setting, error) {
	declared, ok := w.declared[id]
	if !ok {
		return nil, fmt.Errorf("the plugin %q declared nothing", id)
	}
	return declared, nil
}

// schemaOf returns the checked settings of plugin id, an error when the check rejects them.
func (w *settingsWorld) schemaOf(id string) (settings.Schema, error) {
	declared, err := w.declaredBy(id)
	if err != nil {
		return settings.Schema{}, err
	}
	schema, err := settings.Check(id, declared, w.rules)
	if err != nil {
		return settings.Schema{}, fmt.Errorf("the check rejected the settings: %w", err)
	}
	return schema, nil
}

// checked checks the settings of plugin id and keeps the rejection.
func (w *settingsWorld) checked(id string) error {
	declared, err := w.declaredBy(id)
	if err != nil {
		return err
	}
	_, w.err = settings.Check(id, declared, w.rules)
	return nil
}

// workedOut checks the settings of plugin id, then works them out from the environment and the saved values.
func (w *settingsWorld) workedOut(id string) error {
	schema, err := w.schemaOf(id)
	if err != nil {
		return err
	}
	w.schema = schema
	w.values, w.rejected, w.err = schema.Resolve(settings.Sources{Env: w.found, Saved: w.saved})
	return nil
}

// changed checks change against the settings of plugin id and the environment, and keeps its rejection.
func (w *settingsWorld) changed(id string, change map[string]string) error {
	schema, err := w.schemaOf(id)
	if err != nil {
		return err
	}
	w.err = schema.CheckChange(change, w.found)
	return nil
}

// changes checks a change to plugin id that sets key to value.
func (w *settingsWorld) changes(id, key, value string) error {
	return w.changed(id, map[string]string{key: value})
}

// changesTable checks a change to plugin id that sets each key of table to the value beside it.
func (w *settingsWorld) changesTable(id string, table *godog.Table) error {
	change := make(map[string]string, len(table.Rows)-1)
	for _, row := range table.Rows[1:] {
		change[row.Cells[0].Value] = row.Cells[1].Value
	}
	return w.changed(id, change)
}

// variableOf returns the variable that sets key of plugin id.
func (w *settingsWorld) variableOf(id, key string) (string, error) {
	schema, err := w.schemaOf(id)
	if err != nil {
		return "", err
	}
	for _, field := range schema.Fields {
		if field.Key == key {
			return field.Env, nil
		}
	}
	return "", fmt.Errorf("the plugin %q has no setting %q", id, key)
}

// readFrom fails unless key of plugin id is set by variable.
func (w *settingsWorld) readFrom(key, id, variable string) error {
	got, err := w.variableOf(id, key)
	if err != nil {
		return err
	}
	if got != variable {
		return fmt.Errorf("%s of %s is read from %s, want %s", key, id, got, variable)
	}
	return nil
}

// readFromTable fails unless each key of table is set by the variable beside it.
func (w *settingsWorld) readFromTable(id string, table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		if err := w.readFrom(row.Cells[0].Value, id, row.Cells[1].Value); err != nil {
			return err
		}
	}
	return nil
}

// reading returns a resolved value as the text a scenario writes it in.
func reading(value settings.Value) string {
	switch value.Type {
	case settings.TypeCount:
		return strconv.Itoa(value.Count)
	case settings.TypeFlag:
		return strconv.FormatBool(value.Flag)
	case settings.TypeDuration:
		return value.Duration.String()
	case settings.TypeLines:
		return strings.Join(value.Lines, ", ")
	case settings.TypeSecret:
		return value.Secret.String()
	}
	return value.Text
}

// written returns the text a scenario wants a value of kind to read, a duration in the form Duration.String gives.
func written(kind, text string) string {
	if kind != settings.TypeDuration {
		return text
	}
	if d, err := time.ParseDuration(text); err == nil {
		return d.String()
	}
	return text
}

// resolved fails when working out the settings was rejected.
func (w *settingsWorld) resolved() error {
	if w.err != nil {
		return fmt.Errorf("the settings were rejected: %w", w.err)
	}
	return nil
}

// valueOf returns the worked out value of key, an error when the settings were rejected or hold no key.
func (w *settingsWorld) valueOf(key string) (settings.Value, error) {
	if err := w.resolved(); err != nil {
		return settings.Value{}, err
	}
	value, ok := w.values[key]
	if !ok {
		return settings.Value{}, fmt.Errorf("no setting %q was worked out", key)
	}
	return value, nil
}

// reads fails unless the worked out key reads want.
func (w *settingsWorld) reads(key, want string) error {
	value, err := w.valueOf(key)
	if err != nil {
		return err
	}
	if got := reading(value); got != written(value.Type, want) {
		return fmt.Errorf("%s reads %q, want %q", key, got, want)
	}
	return nil
}

// sourceIs fails unless the worked out key came from source.
func (w *settingsWorld) sourceIs(key, source string) error {
	value, err := w.valueOf(key)
	if err != nil {
		return err
	}
	if value.Source != source {
		return fmt.Errorf("%s came from %q, want %q", key, value.Source, source)
	}
	return nil
}

// notWorkedOut fails unless the settings were worked out with no setting under key.
func (w *settingsWorld) notWorkedOut(key string) error {
	if err := w.resolved(); err != nil {
		return err
	}
	if _, ok := w.values[key]; ok {
		return fmt.Errorf("a setting %q was worked out", key)
	}
	return nil
}

// noneRejected fails unless the settings were worked out with no saved value reported as rejected.
func (w *settingsWorld) noneRejected() error {
	if err := w.resolved(); err != nil {
		return err
	}
	if len(w.rejected) != 0 {
		return fmt.Errorf("%d saved values were reported as rejected, the first %q", len(w.rejected), w.rejected[0].Key)
	}
	return nil
}

// onlyRejected fails unless key is the one saved value reported as rejected, with a value error of reason.
func (w *settingsWorld) onlyRejected(key, reason string) error {
	if err := w.resolved(); err != nil {
		return err
	}
	if len(w.rejected) != 1 {
		return fmt.Errorf("%d saved values were reported as rejected, want 1", len(w.rejected))
	}
	report := w.rejected[0]
	var rejection *settings.ValueError
	if !errors.As(report.Err, &rejection) {
		return fmt.Errorf("the saved value %q was reported with %v, want a value error", report.Key, report.Err)
	}
	if report.Key != key || rejection.Reason != reason {
		return fmt.Errorf("the saved value %q was reported as rejected with %q, want %q with %q",
			report.Key, rejection.Reason, key, reason)
	}
	return nil
}

// rejectedLacks fails when any print, log or encoding of a saved value reported as rejected or its error holds text.
func (w *settingsWorld) rejectedLacks(text string) error {
	for _, report := range w.rejected {
		if err := nowhereIn("the error of "+report.Key, report.Err, text); err != nil {
			return err
		}
	}
	return nowhereIn("the saved values reported as rejected", w.rejected, text)
}

// readsTable fails unless the worked out settings are those of table, each reading the value beside it.
func (w *settingsWorld) readsTable(table *godog.Table) error {
	if got, want := len(w.values), len(table.Rows)-1; got != want {
		return fmt.Errorf("%d settings were worked out, want %d", got, want)
	}
	for _, row := range table.Rows[1:] {
		if err := w.reads(row.Cells[0].Value, row.Cells[1].Value); err != nil {
			return err
		}
	}
	return nil
}

// rejectedWith fails unless the last check or working out was rejected with want.
func (w *settingsWorld) rejectedWith(want string) error {
	if w.err == nil {
		return fmt.Errorf("the settings were not rejected, want %q", want)
	}
	if w.err.Error() != want {
		return fmt.Errorf("the settings were rejected with %q, want %q", w.err, want)
	}
	return nil
}

// passed fails unless the last check passed.
func (w *settingsWorld) passed() error {
	if w.err != nil {
		return fmt.Errorf("the check rejected the settings: %w", w.err)
	}
	return nil
}

// reasonLacks fails unless the last rejection is there and no print, log or encoding of it holds text.
func (w *settingsWorld) reasonLacks(text string) error {
	if w.err == nil {
		return errors.New("nothing was rejected")
	}
	return nowhereIn("the reason", w.err, text)
}

// revealed fails unless the secret key reveals want.
func (w *settingsWorld) revealed(key, want string) error {
	if got := w.values[key].Secret.Reveal(); got != want {
		return fmt.Errorf("the secret %s reveals %q, want %q", key, got, want)
	}
	return nil
}

// dropTime leaves the time out of a log line, so two lines compare by what they log.
func dropTime(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return attr
}

// textLine returns the line slog's text handler writes for value under the key setting.
func textLine(value any) string {
	var out bytes.Buffer
	slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{ReplaceAttr: dropTime})).Info("read", "setting", value)
	return out.String()
}

// jsonLine returns the line slog's JSON handler writes for value under the key setting.
func jsonLine(value any) string {
	var out bytes.Buffer
	slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{ReplaceAttr: dropTime})).Info("read", "setting", value)
	return out.String()
}

// encoding returns value encoded by encoding/json, the error text when it fails.
func encoding(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// printer returns value through the fmt verb.
func printer(verb string) func(any) string {
	return func(value any) string { return fmt.Sprintf(verb, value) }
}

// renderers prints, logs and encodes a value in each way a site may show it.
var renderers = map[string]func(any) string{
	"%v": printer("%v"), "%+v": printer("%+v"), "%s": printer("%s"), "%q": printer("%q"),
	"text log": textLine, "JSON log": jsonLine, "encoding/json": encoding,
}

// showsAs fails unless every print and log of the secret key shows as the text want would.
func (w *settingsWorld) showsAs(want, key string) error {
	secret := w.values[key].Secret
	for name, render := range renderers {
		if got, plain := render(secret), render(want); got != plain {
			return fmt.Errorf("%s shows %s as %q, want %q", name, key, got, plain)
		}
	}
	if got := printer("%#v")(secret); got != want {
		return fmt.Errorf("%%#v shows %s as %q, want %q", key, got, want)
	}
	return nil
}

// nowhereIn fails when any print, log or encoding of value, named what, holds text.
func nowhereIn(what string, value any, text string) error {
	all := map[string]func(any) string{"%#v": printer("%#v")}
	for name, render := range renderers {
		all[name] = render
	}
	for name, render := range all {
		if out := render(value); strings.Contains(out, text) {
			return fmt.Errorf("%s of %s holds %q: %s", name, what, text, out)
		}
	}
	return nil
}

// nowhere fails when any print, log or encoding of the worked out settings holds text.
func (w *settingsWorld) nowhere(text string) error {
	return nowhereIn("the settings", w.values, text)
}

// formFieldOf returns the form field of key, described from the worked out settings.
func (w *settingsWorld) formFieldOf(key string) (settings.FormField, error) {
	if err := w.resolved(); err != nil {
		return settings.FormField{}, err
	}
	for _, field := range w.schema.FormFields(w.values) {
		if field.Key == key {
			return field, nil
		}
	}
	return settings.FormField{}, fmt.Errorf("the form shows no field %q", key)
}

// formLocked fails unless the form shows key locked by variable.
func (w *settingsWorld) formLocked(key, variable string) error {
	field, err := w.formFieldOf(key)
	if err != nil {
		return err
	}
	if !field.Locked || field.Env != variable {
		return fmt.Errorf("the form shows %s locked %t by %s, want locked by %s", key, field.Locked, field.Env, variable)
	}
	return nil
}

// formValue fails unless the form shows key with the value want.
func (w *settingsWorld) formValue(key, want string) error {
	field, err := w.formFieldOf(key)
	if err != nil {
		return err
	}
	if field.Value == nil {
		return fmt.Errorf("the form shows %s with no value, want %q", key, want)
	}
	if *field.Value != want {
		return fmt.Errorf("the form shows %s with the value %q, want %q", key, *field.Value, want)
	}
	return nil
}

// formNotSet fails unless the form shows the secret key as not set, with no value.
func (w *settingsWorld) formNotSet(key string) error {
	field, err := w.formFieldOf(key)
	if err != nil {
		return err
	}
	if field.Set == nil || *field.Set {
		return fmt.Errorf("the form shows %s with set %s, want false", key, encoding(field.Set))
	}
	if field.Value != nil {
		return fmt.Errorf("the form shows the secret %s with a value", key)
	}
	return nil
}

// formEncodes fails unless the form fields encode as the JSON of want, the order of keys aside.
func (w *settingsWorld) formEncodes(want *godog.DocString) error {
	if err := w.resolved(); err != nil {
		return err
	}
	data, err := json.Marshal(w.schema.FormFields(w.values))
	if err != nil {
		return err
	}
	var got, wanted any
	if err := errors.Join(json.Unmarshal(data, &got), json.Unmarshal([]byte(want.Content), &wanted)); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, wanted) {
		return fmt.Errorf("the form fields encode as %s, want %s", data, want.Content)
	}
	return nil
}

// formLacks fails when any print, log or encoding of the form fields holds text.
func (w *settingsWorld) formLacks(text string) error {
	if err := w.resolved(); err != nil {
		return err
	}
	return nowhereIn("the form fields", w.schema.FormFields(w.values), text)
}

// changeError returns the rejection of the last change as a change error, an error when it holds none.
func (w *settingsWorld) changeError() (*settings.ChangeError, error) {
	var problem *settings.ChangeError
	if !errors.As(w.err, &problem) {
		return nil, fmt.Errorf("the change was not rejected as a change error: %v", w.err)
	}
	return problem, nil
}

// changeAccepted fails unless the last change was accepted.
func (w *settingsWorld) changeAccepted() error {
	if w.err != nil {
		return fmt.Errorf("the change was rejected: %w", w.err)
	}
	return nil
}

// rejectedAs returns the rejection of the last change, an error unless it is of kind for key.
func (w *settingsWorld) rejectedAs(kind, key string) (*settings.ChangeError, error) {
	problem, err := w.changeError()
	if err != nil {
		return nil, err
	}
	if problem.Kind != kind || problem.Key != key {
		return nil, fmt.Errorf("the change was rejected as %q for %q, want %q for %q", problem.Kind, problem.Key, kind, key)
	}
	return problem, nil
}

// changeRejected fails unless the last change was rejected as kind for key.
func (w *settingsWorld) changeRejected(kind, key string) error {
	_, err := w.rejectedAs(kind, key)
	return err
}

// changeRejectedWith fails unless the last change was rejected as kind for key with reason.
func (w *settingsWorld) changeRejectedWith(kind, key, reason string) error {
	problem, err := w.rejectedAs(kind, key)
	if err != nil {
		return err
	}
	if problem.Reason != reason {
		return fmt.Errorf("the change to %s was rejected with the reason %q, want %q", key, problem.Reason, reason)
	}
	return nil
}

// namesVariable fails unless the rejection of the last change names variable.
func (w *settingsWorld) namesVariable(variable string) error {
	problem, err := w.changeError()
	if err != nil {
		return err
	}
	if problem.Env != variable {
		return fmt.Errorf("the rejection names the variable %q, want %q", problem.Env, variable)
	}
	return nil
}

// givesOnly fails unless the rejection of the last change gives the bound which at limit, and no other bound or option.
func (w *settingsWorld) givesOnly(which string, limit int) error {
	problem, err := w.changeError()
	if err != nil {
		return err
	}
	want := settings.ChangeError{Max: &limit}
	if which == "min" {
		want = settings.ChangeError{Min: &limit}
	}
	got := settings.ChangeError{Min: problem.Min, Max: problem.Max, Options: problem.Options}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("the rejection gives the min %s, the max %s and the options %q, want only the %s %d",
			encoding(problem.Min), encoding(problem.Max), problem.Options, which, limit)
	}
	return nil
}

// givesOptions fails unless the rejection of the last change gives the options of the comma list.
func (w *settingsWorld) givesOptions(list string) error {
	problem, err := w.changeError()
	if err != nil {
		return err
	}
	if want := optionsOf(list); !slices.Equal(problem.Options, want) {
		return fmt.Errorf("the rejection gives the options %q, want %q", problem.Options, want)
	}
	return nil
}

// givenSettings binds the steps that set up a settings scenario.
func givenSettings(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the application names plugin variables under "([^"]*)"$`, w.namesUnder)
	sc.Step(`^the application reserves the key starts "([^"]*)" and "([^"]*)"$`, w.reserves)
	sc.Step(`^the plugin "([^"]*)" declares these settings:$`, w.declaresTable)
	sc.Step(`^the plugin "([^"]*)" declares no settings$`, w.declaresNone)
	sc.Step(`^the plugin "([^"]*)" declares (a .+|the text setting .+)$`, w.declares)
	sc.Step(`^the environment holds "([^"]*)" in "([^"]*)"$`, w.holds)
	sc.Step(`^"([^"]*)" is saved as "([^"]*)"$`, w.savedAs)
}

// whenSettings binds the steps that check and work out the settings and check a change to them.
func whenSettings(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the settings of "([^"]*)" are checked$`, w.checked)
	sc.Step(`^the settings of "([^"]*)" are worked out$`, w.workedOut)
	sc.Step(`^a change to "([^"]*)" sets "([^"]*)" to "([^"]*)"$`, w.changes)
	sc.Step(`^a change to "([^"]*)" sets these settings:$`, w.changesTable)
}

// thenSettings binds the steps that check what the settings read and why they were rejected.
func thenSettings(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the settings of "([^"]*)" are read from these variables:$`, w.readFromTable)
	sc.Step(`^the setting "([^"]*)" of "([^"]*)" is read from "([^"]*)"$`, w.readFrom)
	sc.Step(`^the settings read:$`, w.readsTable)
	sc.Step(`^"([^"]*)" reads "([^"]*)"$`, w.reads)
	sc.Step(`^the settings are rejected with "(.*)"$`, w.rejectedWith)
	sc.Step(`^the check rejects them with "(.*)"$`, w.rejectedWith)
	sc.Step(`^the settings pass the check$`, w.passed)
	sc.Step(`^the reason does not hold "([^"]*)"$`, w.reasonLacks)
	sc.Step(`^the secret "([^"]*)" reads "([^"]*)" when asked for$`, w.revealed)
	sc.Step(`^every print or log of the settings shows "([^"]*)" for "([^"]*)"$`, w.showsAs)
	sc.Step(`^no print, log or encoding of the settings holds "([^"]*)"$`, w.nowhere)
}

// thenSaved binds the steps that check where each value came from and which saved values were reported.
func thenSaved(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the source of "([^"]*)" is "([^"]*)"$`, w.sourceIs)
	sc.Step(`^no setting "([^"]*)" is worked out$`, w.notWorkedOut)
	sc.Step(`^no saved value is reported as rejected$`, w.noneRejected)
	sc.Step(`^the only saved value reported as rejected is "([^"]*)", with the reason "([^"]*)"$`, w.onlyRejected)
	sc.Step(`^no saved value reported as rejected holds "([^"]*)"$`, w.rejectedLacks)
}

// thenForm binds the steps that check how each field is described for a settings form.
func thenForm(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the form shows "([^"]*)" locked by "([^"]*)"$`, w.formLocked)
	sc.Step(`^the form shows "([^"]*)" with the value "([^"]*)"$`, w.formValue)
	sc.Step(`^the form shows "([^"]*)" as not set$`, w.formNotSet)
	sc.Step(`^the form fields encode as:$`, w.formEncodes)
	sc.Step(`^no encoding of the form fields holds "([^"]*)"$`, w.formLacks)
}

// thenChange binds the steps that check whether a change was accepted and why it was rejected.
func thenChange(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the change is accepted$`, w.changeAccepted)
	sc.Step(`^the change is rejected as "([^"]*)" for "([^"]*)"$`, w.changeRejected)
	sc.Step(`^the change is rejected as "([^"]*)" for "([^"]*)" with the reason "([^"]*)"$`, w.changeRejectedWith)
	sc.Step(`^the rejection names the variable "([^"]*)"$`, w.namesVariable)
	sc.Step(`^the rejection gives only the (min|max) (-?\d+)$`, w.givesOnly)
	sc.Step(`^the rejection gives the options "([^"]*)"$`, w.givesOptions)
}

// initializeSettings gives each scenario of the settings features a world of its own and its steps.
func initializeSettings() func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &settingsWorld{declared: map[string][]wire.Setting{}, found: map[string]string{}, saved: map[string]string{}}
		givenSettings(sc, w)
		whenSettings(sc, w)
		thenSettings(sc, w)
		thenSaved(sc, w)
		thenForm(sc, w)
		thenChange(sc, w)
	}
}
