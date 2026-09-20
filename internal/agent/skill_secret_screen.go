package agent

import (
	"regexp"
)

// skill_secret_screen.go — Narrower secret screening for skill content.
//
// Unlike containsSecretPattern (which blocks on keyword presence like
// "password"), this checks for VALUE-SHAPED patterns: actual secrets
// embedded in the content, not concept mentions.
//
// A skill about auth flows that says "set the password field" should
// pass. A skill with "password:Hunter2" or "Bearer eyJhbGc..." should
// be refused.

// secretValuePatterns matches patterns that look like actual embedded
// secrets, not conceptual mentions. Each pattern requires a value
// following the keyword, not just the keyword alone.
var secretValuePatterns = []*regexp.Regexp{
	// API key with value: api_key="..." , api_key: ..., api-key=...
	regexp.MustCompile(`(?i)(?:api[_-]?key|apikey)\s*[:=]\s*\S+`),
	// Secret key with value: secret_key="...", secretkey: ...
	regexp.MustCompile(`(?i)(?:secret[_-]?key|secretkey)\s*[:=]\s*\S+`),
	// Access token with value: access_token="...", access_token: ...
	regexp.MustCompile(`(?i)(?:access[_-]?token|accesstoken)\s*[:=]\s*\S+`),
	// Password with value: password="...", password: Hunter2, passwd=...
	// BUT NOT "password field" or "password reset" (word boundary check)
	regexp.MustCompile(`(?i)(?:password|passwd)\s*[:=]\s*\S+`),
	// Bearer token: Bearer eyJ... (must have token value, not just "bearer ")
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{10,}`),
	// Authorization header with value: Authorization: Bearer ..., Authorization: Basic ...
	regexp.MustCompile(`(?i)authorization\s*:\s*\S+`),
	// Private key block: -----BEGIN ... PRIVATE KEY-----
	regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----`),
	// AWS credentials with value (matches aws_secret_access_key, aws_access_key_id, etc.)
	regexp.MustCompile(`(?i)aws[_-]?(?:secret|access)[a-z_-]*(?:key|token)\s*[:=]\s*\S+`),
	// Client secret with value
	regexp.MustCompile(`(?i)client[_-]?secret\s*[:=]\s*\S+`),
	// Token assignment with value: token="...", token: eyJ...
	// Requires 10+ chars to avoid matching "token: false" etc.
	regexp.MustCompile(`(?i)token\s*[:=]\s*([A-Za-z0-9._\-+/=]{10,})`),
	// Generic long base64/hex string that looks like a secret (40+ hex chars or 40+ base64 chars on a line by themselves)
	// This catches pasted API keys that don't have a label.
	regexp.MustCompile(`(?m)^[A-Fa-f0-9]{40,}$`),         // hex (SHA1, etc.)
	regexp.MustCompile(`(?m)^[A-Za-z0-9+/]{40,}={0,2}$`), // base64
}

// containsSkillSecret reports whether skill content contains what looks
// like an actual embedded secret (not just a conceptual mention of
// "password" or "token" in documentation).
func containsSkillSecret(content string) bool {
	for _, p := range secretValuePatterns {
		if p.MatchString(content) {
			return true
		}
	}
	return false
}

// screenSkillSecrets checks skill content for embedded secrets and
// returns a non-empty error message if found, "" if clean.
func screenSkillSecrets(content string) string {
	if containsSkillSecret(content) {
		return "skill content appears to contain embedded secrets (API keys, tokens, passwords, or private keys). Refusing to persist secrets to the global skill store. Remove the secret values and try again."
	}
	return ""
}
