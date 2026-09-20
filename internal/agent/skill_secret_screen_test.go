package agent

import (
	"testing"
)

func TestContainsSkillSecret_APIKeyWithValue(t *testing.T) {
	content := "api_key: sk-1234567890abcdef\nUse this for requests."
	if !containsSkillSecret(content) {
		t.Error("api_key with value should be flagged")
	}
}

func TestContainsSkillSecret_APIKeyConcept(t *testing.T) {
	content := "Set the api_key field in the config file to your key."
	if containsSkillSecret(content) {
		t.Error("api_key as concept (no value) should NOT be flagged")
	}
}

func TestContainsSkillSecret_PasswordWithValue(t *testing.T) {
	content := "password: Hunter2\nThen connect to the database."
	if !containsSkillSecret(content) {
		t.Error("password with value should be flagged")
	}
}

func TestContainsSkillSecret_PasswordConcept(t *testing.T) {
	content := "# Password Reset Flow\n\nThe password reset flow works as follows..."
	if containsSkillSecret(content) {
		t.Error("password as concept should NOT be flagged")
	}
}

func TestContainsSkillSecret_BearerToken(t *testing.T) {
	content := "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	if !containsSkillSecret(content) {
		t.Error("Bearer token with JWT should be flagged")
	}
}

func TestContainsSkillSecret_BearerConcept(t *testing.T) {
	content := "Use a bearer token for authentication."
	if containsSkillSecret(content) {
		t.Error("bearer as concept (no token value) should NOT be flagged")
	}
}

func TestContainsSkillSecret_PrivateKeyBlock(t *testing.T) {
	content := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA..."
	if !containsSkillSecret(content) {
		t.Error("private key block should be flagged")
	}
}

func TestContainsSkillSecret_AWSCredentials(t *testing.T) {
	content := "aws_secret_access_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	if !containsSkillSecret(content) {
		t.Error("AWS secret with value should be flagged")
	}
}

func TestContainsSkillSecret_ClientSecretWithValue(t *testing.T) {
	content := "client_secret: my-super-secret-value-12345"
	if !containsSkillSecret(content) {
		t.Error("client_secret with value should be flagged")
	}
}

func TestContainsSkillSecret_TokenWithValue(t *testing.T) {
	content := "token: eyJhbGciOiJIUzI1NiJ9eyJzdWIi"
	if !containsSkillSecret(content) {
		t.Error("token with 10+ char value should be flagged")
	}
}

func TestContainsSkillSecret_TokenShortValue(t *testing.T) {
	content := "token: false"
	if containsSkillSecret(content) {
		t.Error("token: false (short value) should NOT be flagged")
	}
}

func TestContainsSkillSecret_HexString(t *testing.T) {
	content := "Some skill content\n\na1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2\n\nMore content."
	if !containsSkillSecret(content) {
		t.Error("40+ char hex string on its own line should be flagged")
	}
}

func TestContainsSkillSecret_NormalSkillContent(t *testing.T) {
	content := `# Git Flow Skill

## How to create a feature branch

1. Run git checkout -b feature/my-feature
2. Make your changes
3. Run tests with make test
4. Create a PR

## Notes
- Always use conventional commits
- Squash commits before merging`
	if containsSkillSecret(content) {
		t.Error("normal skill content should NOT be flagged")
	}
}

func TestContainsSkillSecret_AuthRunbook(t *testing.T) {
	content := `# Authentication Setup

To set up authentication:
1. Configure the password field in the config
2. Set the token expiration to 24 hours
3. Use the authorization header for API requests
4. Store the API key in the environment

Note: Never commit secrets to the repo.`
	if containsSkillSecret(content) {
		t.Error("auth runbook mentioning concepts should NOT be flagged")
	}
}

func TestScreenSkillSecrets_Clean(t *testing.T) {
	if msg := screenSkillSecrets("normal content"); msg != "" {
		t.Errorf("clean content should return empty, got %q", msg)
	}
}

func TestScreenSkillSecrets_Dirty(t *testing.T) {
	msg := screenSkillSecrets("api_key: sk-live-1234567890abcdef")
	if msg == "" {
		t.Error("content with secret should return non-empty message")
	}
	if !contains(msg, "secret") {
		t.Errorf("message should mention 'secret', got %q", msg)
	}
}
