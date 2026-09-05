package recordruntime

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/generalbusiness-ai/tailapps/jsonataddl"
)

func TestPinnedIdentity(t *testing.T) {
	identity, err := Identity()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"dialect.txt":  GitseqRecord().Canonical(),
		"identity.txt": identity.Descriptor() + "\n" + identity.Digest() + "\n",
	} {
		want, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s changed; review the runtime contract and binding before updating the pin:\n%s", name, got)
		}
	}
}

func TestEveryComponentChangesIdentity(t *testing.T) {
	base, err := Identity()
	if err != nil {
		t.Fatal(err)
	}
	for index, component := range components() {
		t.Run(component.Key, func(t *testing.T) {
			changed := components()
			changed[index].Value += "-changed"
			identity, err := jsonataddl.ComposeIdentity(changed...)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Digest() == base.Digest() {
				t.Fatal("semantic component change preserved runtime identity")
			}
		})
	}
}

func TestEveryLimitChangesIdentity(t *testing.T) {
	base := jsonataddl.DialectComponent(GitseqRecord())
	limits := reflect.TypeFor[jsonataddl.Limits]()
	for index := range limits.NumField() {
		t.Run(limits.Field(index).Name, func(t *testing.T) {
			changed := GitseqRecord()
			field := reflect.ValueOf(&changed.Limits).Elem().Field(index)
			field.SetInt(field.Int() + 1)
			if jsonataddl.DialectComponent(changed) == base {
				t.Fatal("limit change preserved dialect component")
			}
		})
	}
}

// This deliberately small policy probe is not the inventory application or
// a copy of the upstream corpus. It proves this host supplies the dialect to
// the external core, including its admission and emission limits.
func policySource(normalizer string) fstest.MapFS {
	return fstest.MapFS{
		"application.sql": {Data: []byte(`CREATE EVENT inventory_event (id TEXT NOT NULL);
CREATE TABLE seen (id TEXT NOT NULL, PRIMARY KEY (id));
CREATE NORMALIZER normalize ON gitseq_record USING 'folds/normalize.jsonata' EMITS inventory_event;
CREATE FOLD apply ON inventory_event USING 'folds/apply.jsonata' WRITES seen;
CREATE EXPORT seen AS SELECT id FROM seen;`)},
		"folds/normalize.jsonata": {Data: []byte(normalizer)},
		"folds/apply.jsonata":     {Data: []byte(`{"decision":"effective","facts":[],"tables":{}}`)},
	}
}

func TestCoreEnforcesHostPolicy(t *testing.T) {
	identity, err := Identity()
	if err != nil {
		t.Fatal(err)
	}
	const result = `{"decision":"effective","facts":[],"tables":{},"events":{"inventory_event":%s}}`
	for _, test := range []struct{ name, events, diagnostic string }{
		{"one event", `[{"id":"one"}]`, ""},
		{"two events", `[{"id":"one"},{"id":"two"}]`, "normalized events exceed 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := policySource(strings.Replace(result, "%s", test.events, 1))
			app, err := jsonataddl.LoadApplication(sources, ".", "policy-probe", GitseqRecord(), identity.Digest())
			if err != nil {
				t.Fatal(err)
			}
			input, err := recordInput(unitLog(`{"id":"a","qty":1,"sku":"ink"}`).Records[0], 1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := app.Evaluate("normalize", input)
			if test.diagnostic == "" {
				if err != nil || got.Decision != "effective" || len(got.Events["inventory_event"]) != 1 {
					t.Fatalf("one-event result = %#v, %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("error = %v, want %q", err, test.diagnostic)
			}
		})
	}
	for _, expression := range []string{`$now()`, `*`, `$eval("1")`} {
		if _, err := jsonataddl.LoadApplication(policySource(expression), ".", "policy-probe", GitseqRecord(), identity.Digest()); err == nil {
			t.Errorf("admitted ambient or order-dependent expression %q", expression)
		}
	}
}
