// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/framework/pluginkit/settings"
	"github.com/gopherium/framework/pluginkit/wire"
)

// settingsWorld is what one scenario of the settings features declares, finds and works out.
type settingsWorld struct {
	rules    settings.Rules
	declared map[string][]wire.Setting
	found    map[string]string
	values   settings.Values
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

// settingOf reads the cells of one row of a settings table under columns.
func settingOf(cells []string, columns map[string]int) (wire.Setting, error) {
	cell := func(name string) string { return cells[columns[name]] }
	low, lowErr := boundOf(cell("min"))
	high, highErr := boundOf(cell("max"))
	setting := wire.Setting{
		Key: cell("key"), Type: cell("type"), Default: cell("default"),
		Min: low, Max: high, Options: optionsOf(cell("options")),
	}
	return setting, errors.Join(lowErr, highErr)
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

// workedOut checks the settings of plugin id, then works them out from the environment and keeps what it answers.
func (w *settingsWorld) workedOut(id string) error {
	schema, err := w.schemaOf(id)
	if err != nil {
		return err
	}
	w.values, w.err = schema.Resolve(w.found)
	return nil
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

// reads fails unless the worked out key reads want.
func (w *settingsWorld) reads(key, want string) error {
	if w.err != nil {
		return fmt.Errorf("the settings were rejected: %w", w.err)
	}
	value, ok := w.values[key]
	if !ok {
		return fmt.Errorf("no setting %q was worked out", key)
	}
	if got := reading(value); got != written(value.Type, want) {
		return fmt.Errorf("%s reads %q, want %q", key, got, want)
	}
	return nil
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

// reasonLacks fails unless the last rejection is there and does not hold text.
func (w *settingsWorld) reasonLacks(text string) error {
	if w.err == nil {
		return errors.New("nothing was rejected")
	}
	if strings.Contains(w.err.Error(), text) {
		return fmt.Errorf("the reason %q holds %q", w.err, text)
	}
	return nil
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

// nowhere fails when any print, log or encoding of the worked out settings holds text.
func (w *settingsWorld) nowhere(text string) error {
	all := map[string]func(any) string{"%#v": printer("%#v")}
	for name, render := range renderers {
		all[name] = render
	}
	for name, render := range all {
		if out := render(w.values); strings.Contains(out, text) {
			return fmt.Errorf("%s of the settings holds %q: %s", name, text, out)
		}
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
}

// whenSettings binds the steps that check and work out the settings.
func whenSettings(sc *godog.ScenarioContext, w *settingsWorld) {
	sc.Step(`^the settings of "([^"]*)" are checked$`, w.checked)
	sc.Step(`^the settings of "([^"]*)" are worked out$`, w.workedOut)
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

// initializeSettings gives each scenario of the settings features a world of its own and its steps.
func initializeSettings() func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &settingsWorld{declared: map[string][]wire.Setting{}, found: map[string]string{}}
		givenSettings(sc, w)
		whenSettings(sc, w)
		thenSettings(sc, w)
	}
}
