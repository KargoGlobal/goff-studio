package config

import (
	"strings"
	"testing"
)

const tokenHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func withAutomation(t *testing.T, extra string) string {
	t.Helper()
	return base(t) + extra
}

func TestAPITokensNotificationsAndMCPLoad(t *testing.T) {
	cfg, err := Load(write(t, withAutomation(t, `
apiTokens:
  - name: incident-bot
    sha256: "sha256:`+strings.ToUpper(tokenHash)+`"
    groups: [incident-responders]
notifications:
  - url: https://hooks.example.com/flags
    environments: [production]
    secret: shh
  - url: https://chat.example.com/hook
    format: Slack
mcp:
  enabled: true
`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.APITokens[0].SHA256; got != tokenHash {
		t.Errorf("hash should be normalised to lowercase hex without a prefix, got %q", got)
	}
	if cfg.Notifications[0].Format != NotifyJSON || cfg.Notifications[1].Format != NotifySlack {
		t.Errorf("formats not normalised: %+v", cfg.Notifications)
	}
	if !cfg.MCP.Enabled {
		t.Error("mcp.enabled not read")
	}
	if !cfg.Notifications[0].NotifiesFor("production") || cfg.Notifications[0].NotifiesFor("dev") {
		t.Error("environment filter not honoured")
	}
	if !cfg.Notifications[1].NotifiesFor("dev") {
		t.Error("a hook with no environments should cover all of them")
	}
}

func TestAutomationEnvironmentOverrides(t *testing.T) {
	t.Setenv("GOFF_STUDIO_API_TOKENS", `[{name: bot, sha256: `+tokenHash+`, groups: [ops]}]`)
	t.Setenv("GOFF_STUDIO_NOTIFICATIONS", `[{url: "https://hooks.example.com/x", format: slack}]`)
	t.Setenv("GOFF_STUDIO_MCP_ENABLED", "true")

	cfg, err := Load(write(t, withAutomation(t, `
apiTokens:
  - name: from-file
    sha256: `+strings.Repeat("a", 64)+`
    groups: [x]
`)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.APITokens) != 1 || cfg.APITokens[0].Name != "bot" {
		t.Errorf("the env var should replace the file's tokens, got %+v", cfg.APITokens)
	}
	if len(cfg.Notifications) != 1 || cfg.Notifications[0].Format != NotifySlack {
		t.Errorf("notifications not overridden: %+v", cfg.Notifications)
	}
	if !cfg.MCP.Enabled {
		t.Error("GOFF_STUDIO_MCP_ENABLED not applied")
	}
}

func TestAPITokenValidation(t *testing.T) {
	cases := []struct {
		name, token string
		want        []string
	}{
		{"plaintext token", `{name: bot, sha256: my-secret-token, groups: [ops]}`, []string{"apiTokens[0].sha256", "never the token itself"}},
		{"no groups", `{name: bot, sha256: ` + tokenHash + `}`, []string{"apiTokens[0].groups"}},
		{"wildcard group", `{name: bot, sha256: ` + tokenHash + `, groups: ["*"]}`, []string{"real groups"}},
		{"bad name", `{name: "bad name", sha256: ` + tokenHash + `, groups: [ops]}`, []string{"apiTokens[0].name"}},
		{"bad email", `{name: bot, sha256: ` + tokenHash + `, groups: [ops], email: nobody}`, []string{"apiTokens[0].email"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := loadErr(t, withAutomation(t, "\napiTokens:\n  - "+tc.token+"\n"))
			assertMentions(t, msg, append(tc.want, "GOFF_STUDIO_API_TOKENS")...)
		})
	}
}

func TestAPITokensMustBeDistinct(t *testing.T) {
	msg := loadErr(t, withAutomation(t, `
apiTokens:
  - {name: a, sha256: `+tokenHash+`, groups: [ops]}
  - {name: b, sha256: `+tokenHash+`, groups: [ops]}
`))
	assertMentions(t, msg, "apiTokens[1].sha256", "needs a token of its own")

	msg = loadErr(t, withAutomation(t, `
apiTokens:
  - {name: a, sha256: `+tokenHash+`, groups: [ops]}
  - {name: a, sha256: `+strings.Repeat("b", 64)+`, groups: [ops]}
`))
	assertMentions(t, msg, "apiTokens[1].name", "repeats")
}

func TestNotificationValidation(t *testing.T) {
	msg := loadErr(t, withAutomation(t, "\nnotifications:\n  - {url: \"\"}\n"))
	assertMentions(t, msg, "notifications[0].url", "GOFF_STUDIO_NOTIFICATIONS")

	msg = loadErr(t, withAutomation(t, "\nnotifications:\n  - {url: \"not a url\"}\n"))
	assertMentions(t, msg, "notifications[0].url")

	msg = loadErr(t, withAutomation(t, "\nnotifications:\n  - {url: \"https://x.example.com\", format: xml}\n"))
	assertMentions(t, msg, "notifications[0].format", "xml")
}

func TestPlaintextNotificationWarns(t *testing.T) {
	cfg, err := Load(write(t, withAutomation(t, "\nnotifications:\n  - {url: \"http://hooks.example.com\"}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings(), "\n"), "plaintext") {
		t.Errorf("expected a plaintext warning, got %v", cfg.Warnings())
	}
}

func TestMCPWithoutTokensWarns(t *testing.T) {
	cfg, err := Load(write(t, withAutomation(t, "\nmcp:\n  enabled: true\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings(), "\n"), "no apiTokens") {
		t.Errorf("expected a warning about missing tokens, got %v", cfg.Warnings())
	}
}
