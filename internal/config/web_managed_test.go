package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const managedWebConfig = `project: {id: 11111111-1111-4111-8111-111111111111}
build:
  env: {BUILD_MODE: test}
  profiles:
    preview:
      web:
        app_id: 55555555-5555-4555-8555-555555555555
        setup_commands: [npm ci]
        build_commands: [npm run build]
        output_path: dist
`

func TestManagedWebConfigCompilation(t *testing.T) {
	for _, runtime := range []string{"", "        run: {start: node server.js, port: 8080, healthcheck: /health, start_timeout_seconds: 90, env_keys: [APP_URL], secret_names: [API_TOKEN]}\n"} {
		authored, err := ParseAuthoredConfig([]byte(managedWebConfig + runtime))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalCanonicalConfig(*authored)
		if err != nil {
			t.Fatal(err)
		}
		aggregate, err := CompileConfigBytes(encoded, CompilationContext{"apps/web", "apps/web"})
		if err != nil {
			t.Fatal(err)
		}
		if len(aggregate.Profiles) != 1 || len(aggregate.Profiles[0].Configurations) != 1 {
			t.Fatalf("profiles = %#v", aggregate.Profiles)
		}
		configuration := aggregate.Profiles[0].Configurations[0]
		if configuration.Platform != "web" || configuration.Recipe.Framework != "web" || configuration.Recipe.Run == nil {
			t.Fatalf("configuration = %#v", configuration)
		}
		if err := ValidateExecutionRecipe(*configuration.Recipe); err != nil {
			t.Fatal(err)
		}
		if runtime == "" && !reflect.DeepEqual(*configuration.Recipe.Run, DefaultWebRun()) {
			t.Fatalf("run defaults = %#v", configuration.Recipe.Run)
		}
		if runtime == "" {
			if configuration.BuildDefinitionHash != "e418c53856dbcb62cf964b5bd9049a7e88c3e725952bc96e7480fdfa934d563b" || aggregate.Profiles[0].BuildProfileHash != "9f2bcf56566fe107f677ff8c79eb37a83a935965a45957dc2be5fc0b41df7165" || aggregate.ProjectConfigurationHash != "6085208f453d50e585852fc202eab5c0db3a31e57c5ac57385d380f52d7ca270" {
				t.Fatalf("Web hashes do not match the Python compiler: %#v", aggregate)
			}
		}
		if runtime != "" && (configuration.Recipe.Run.Port != 8080 || *configuration.Recipe.Run.Start != "node server.js") {
			t.Fatalf("custom run = %#v", configuration.Recipe.Run)
		}
		if runtime != "" {
			if configuration.BuildDefinitionHash != "f02f9d9a4bc94265a7ada4501790e591c44d9609194fd5aed55c03478dbddfea" || aggregate.Profiles[0].BuildProfileHash != "05c8692348b548f08711d09f5150c6466853c27fb16bbeb676740e36407a4df1" || aggregate.ProjectConfigurationHash != "74e2415f94ceb16ce5d17caead6adc53706a558b0fe6230df86e2bdccd2008a2" {
				t.Fatalf("SSR Web hashes do not match the Python compiler: %#v", aggregate)
			}
		}
		selection, err := ResolveProfilePlatform(*aggregate, "preview", "web", false)
		if err != nil || selection.Resolved == nil {
			t.Fatalf("selection = %#v, %v", selection, err)
		}
	}
}

func TestManagedWebRuntimeContractErrors(t *testing.T) {
	for _, runtime := range []string{
		"{start: ''}", "{start: null, port: 0}", "{port: 65536}", "{port: null}",
		"{start_timeout_seconds: 9}", "{start_timeout_seconds: 601}",
		"{healthcheck: https://example.com}", "{healthcheck: '//example.com'}", "{healthcheck: null}",
		"{env_keys: [TOKEN=value]}", "{env_keys: [TOKEN, TOKEN]}",
		"{env_keys: [TOKEN], secret_names: [TOKEN]}", "{secret_names: [bad-name]}",
		"{env: {TOKEN: value}}",
	} {
		t.Run(runtime, func(t *testing.T) {
			if _, err := CompileConfigBytes([]byte(managedWebConfig+"        run: "+runtime+"\n"), CompilationContext{".", "."}); err == nil {
				t.Fatal("invalid runtime accepted")
			}
		})
	}
}

func TestManagedWebOutputPathValidation(t *testing.T) {
	for _, output := range []string{".", "..", "../dist", "dist/../other", "/dist", "dist/*", `C:\dist`, `dist\outside`} {
		t.Run(output, func(t *testing.T) {
			raw := strings.Replace(managedWebConfig, "output_path: dist", "output_path: '"+output+"'", 1)
			if _, err := CompileConfigBytes([]byte(raw), CompilationContext{".", "."}); err == nil {
				t.Fatal("unsafe output accepted")
			}
		})
	}
}

func TestManagedWebPartialRecipeRequiresCommandsAndOutput(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(managedWebConfig, "        build_commands: [npm run build]\n", "", 1),
		strings.Replace(managedWebConfig, "        output_path: dist\n", "", 1),
	} {
		if _, err := CompileConfigBytes([]byte(raw), CompilationContext{".", "."}); err == nil {
			t.Fatal("incomplete build accepted")
		}
	}
	for _, partial := range []string{"setup_commands: []", "env: {}", "secrets: []", "caches: []", "output_path: null"} {
		raw := "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  profiles:\n    preview:\n      web: {" + partial + "}\n"
		if _, err := ParseAuthoredConfig([]byte(raw)); err == nil {
			t.Fatalf("partial recipe %s was silently discarded", partial)
		}
	}
}

func TestWebAppOnlyJSONKeepsURLContract(t *testing.T) {
	appID := webRecipeTestAppID
	encoded, err := json.Marshal(AuthoredWebRecipe{AppID: &appID})
	if err != nil || string(encoded) != `{"app_id":"55555555-5555-4555-8555-555555555555"}` {
		t.Fatalf("app-only JSON = %s, %v", encoded, err)
	}
}

func TestManagedWebRunChangesDefinitionHash(t *testing.T) {
	first, err := CompileConfigBytes([]byte(managedWebConfig), CompilationContext{".", "."})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileConfigBytes([]byte(managedWebConfig+"        run: {env_keys: [APP_URL]}\n"), CompilationContext{".", "."})
	if err != nil {
		t.Fatal(err)
	}
	if first.Profiles[0].Configurations[0].BuildDefinitionHash == second.Profiles[0].Configurations[0].BuildDefinitionHash {
		t.Fatal("runtime contract did not affect build hash")
	}
	encoded, err := json.Marshal(first.Profiles[0].Configurations[0].Recipe)
	if err != nil || !strings.Contains(string(encoded), `"env_keys":[]`) {
		t.Fatalf("normalized runtime = %s, %v", encoded, err)
	}
}

func TestManagedWebRuntimePublicationVariables(t *testing.T) {
	for _, test := range []struct {
		name, runtime, code, field string
	}{
		{name: "correct types", runtime: "{env_keys: [PLAIN], secret_names: [SECRET]}"},
		{name: "unknown env", runtime: "{env_keys: [MISSING]}", code: "launch_var_not_available_for_organization", field: "env_keys"},
		{name: "unknown secret", runtime: "{secret_names: [MISSING]}", code: "secret_not_available_for_organization", field: "secret_names"},
		{name: "secret as plaintext", runtime: "{env_keys: [SECRET]}", code: "launch_var_not_available_for_organization", field: "env_keys"},
		{name: "plaintext as secret", runtime: "{secret_names: [PLAIN]}", code: "secret_not_available_for_organization", field: "secret_names"},
	} {
		t.Run(test.name, func(t *testing.T) {
			aggregate, err := CompileConfigBytes([]byte(managedWebConfig+"        run: "+test.runtime+"\n"), CompilationContext{".", "."})
			if err != nil {
				t.Fatal(err)
			}
			err = ValidatePublication(*aggregate, PublicationValidationContext{
				ActiveAppIDs: map[string]struct{}{webRecipeTestAppID: {}}, AvailableLaunchVars: map[string]struct{}{"PLAIN": {}, "SECRET": {}}, AvailableSecretRefs: map[string]struct{}{"SECRET": {}},
			})
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var configError *ConfigError
			if !errors.As(err, &configError) || configError.Code != test.code || !reflect.DeepEqual(configError.Path, []string{"build", "profiles", "preview", "web", "run", test.field}) {
				t.Fatalf("publication error = %#v, want %s", err, test.code)
			}
		})
	}
}
