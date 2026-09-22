package credentials

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// password is built rather than written: a credential-shaped literal in a
// fixture is what this repository forbids, and this file is about not leaking
// one anyway.
var password = "stand-in-" + "password"

func TestSecretNeverRenders(t *testing.T) {
	secret := NewSecret(password)
	asJSON, err := json.Marshal(struct{ Password Secret }{secret})
	if err != nil {
		t.Fatal(err)
	}
	asYAML, err := yaml.Marshal(map[string]Secret{"password": secret})
	if err != nil {
		t.Fatal(err)
	}
	renderings := []string{
		secret.String(),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%+v", struct{ Password Secret }{secret}),
		string(asJSON),
		string(asYAML),
	}
	for _, got := range renderings {
		if strings.Contains(got, password) {
			t.Errorf("a rendering leaked the secret: %s", got)
		}
	}
	if secret.Reveal() != password {
		t.Error("Reveal did not return the value")
	}
	if !NewSecret("").Empty() {
		t.Error("an empty secret is not reported as empty")
	}
}

func TestKeys(t *testing.T) {
	tests := []struct{ key, env string }{
		{key: DBKey("primary"), env: "DBMAP_SECRET_DB_PRIMARY"},
		{key: LLMKey("main"), env: "DBMAP_SECRET_LLM_MAIN"},
		{key: DBKey("west-1.reader"), env: "DBMAP_SECRET_DB_WEST_1_READER"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := EnvName(tt.key); got != tt.env {
				t.Errorf("env name = %q, want %q", got, tt.env)
			}
		})
	}
	if !strings.HasPrefix(DBKey("primary"), "db:") {
		t.Error("db keys are not namespaced")
	}
	if !strings.HasPrefix(LLMKey("main"), "llm:") {
		t.Error("llm keys are not namespaced")
	}
}
