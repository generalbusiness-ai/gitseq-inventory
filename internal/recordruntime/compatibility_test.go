package recordruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	jsonata "github.com/jsonata-go/jsonata/v206"
)

// Corpus B retains the original 18 cases and live 2.0.6 reference, without
// importing the retired spike compiler or treating map iteration as authority.
func TestCorpusB(t *testing.T) {
	source, err := os.ReadFile("testdata/compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Reference string `json:"reference"`
		Cases     []struct {
			Name, Expression, Class string
			Input, Reference        any
		} `json:"cases"`
	}
	if err := json.Unmarshal(source, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Reference != "jsonata-js 2.0.6" || len(corpus.Cases) != 18 {
		t.Fatal("reference identity or case inventory changed")
	}
	module, err := exec.Command("go", "list", "-m", "-json", "github.com/jsonata-go/jsonata").Output()
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		Version, Dir string
		Replace      any
	}
	if err := json.Unmarshal(module, &pin); err != nil {
		t.Fatal(err)
	}
	if pin.Version != "v0.0.0-20250709164031-599f35f32e5f" || pin.Replace != nil {
		t.Fatal("JSONata reference pin changed")
	}
	command := exec.Command("node", filepath.Join("testdata", "reference.js"), pin.Dir)
	command.Stdin = bytes.NewReader(source)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live reference: %v: %s", err, output)
	}
	var reference map[string]any
	if err := json.Unmarshal(output, &reference); err != nil {
		t.Fatal(err)
	}
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			switch test.Class {
			case "portable", "admitted-portable":
				if !reflect.DeepEqual(reference[test.Name], test.Reference) {
					t.Fatal("live reference differs from frozen case")
				}
				program, err := jsonata.Compile(test.Expression, false)
				if err != nil {
					t.Fatal(err)
				}
				program.SetMaxDepth(64)
				program.SetMaxRange(4096)
				input, err := json.Marshal(test.Input)
				if err != nil {
					t.Fatal(err)
				}
				for range 16 {
					result, err := program.Evaluate(input, nil)
					if err != nil {
						t.Fatal(err)
					}
					var got any
					if err := json.Unmarshal(result, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, reference[test.Name]) {
						t.Fatalf("Go/reference mismatch: %s", result)
					}
				}
			case "order-dependent", "admitted-order-dependent", "environment-dependent":
				if _, err := loadSource(policySource(test.Expression), "corpus-b"); err == nil {
					t.Fatal("core admitted ambient or order-dependent expression")
				}
			default:
				t.Fatalf("unknown corpus class %q", test.Class)
			}
		})
	}
	// These ambient/dynamic forms are named in CORPUS.md outside its cases.
	for _, expression := range []string{`$millis()`, `$eval("1")`} {
		t.Run(strings.TrimSuffix(expression, "()"), func(t *testing.T) {
			if _, err := loadSource(policySource(expression), "corpus-b"); err == nil {
				t.Fatal("core admitted ambient or dynamic expression")
			}
		})
	}
}
