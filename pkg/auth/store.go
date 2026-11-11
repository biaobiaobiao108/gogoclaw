package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/credential"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

type AuthCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
	AuthMethod   string    `json:"auth_method"`
	Email        string    `json:"email,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
}

type AuthStore struct {
	Credentials map[string]*AuthCredential `json:"credentials"`
}

const (
	providerGoogleAntigravity = "google-antigravity"
	providerAntigravityAlias  = "antigravity"
)

func (c *AuthCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

func (c *AuthCredential) NeedsRefresh() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

func authFilePath() string {
	return filepath.Join(config.GetHome(), "auth.json")
}

func canonicalProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case providerAntigravityAlias:
		return providerGoogleAntigravity
	default:
		return normalized
	}
}

func cloneCredential(cred *AuthCredential) *AuthCredential {
	if cred == nil {
		return nil
	}
	cp := *cred
	return &cp
}

func mergeCredentials(primary, secondary *AuthCredential) *AuthCredential {
	if primary == nil {
		return cloneCredential(secondary)
	}

	merged := *primary
	if secondary == nil {
		return &merged
	}
	if merged.AccessToken == "" {
		merged.AccessToken = secondary.AccessToken
	}
	if merged.RefreshToken == "" {
		merged.RefreshToken = secondary.RefreshToken
	}
	if merged.AccountID == "" {
		merged.AccountID = secondary.AccountID
	}
	if merged.ExpiresAt.IsZero() {
		merged.ExpiresAt = secondary.ExpiresAt
	}
	if merged.Provider == "" {
		merged.Provider = secondary.Provider
	}
	if merged.AuthMethod == "" {
		merged.AuthMethod = secondary.AuthMethod
	}
	if merged.Email == "" {
		merged.Email = secondary.Email
	}
	if merged.ProjectID == "" {
		merged.ProjectID = secondary.ProjectID
	}

	return &merged
}

func shouldPreferCredential(
	candidate *AuthCredential,
	candidateCanonical bool,
	current *AuthCredential,
	currentCanonical bool,
) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}

	switch {
	case candidate.ExpiresAt.After(current.ExpiresAt):
		return true
	case current.ExpiresAt.After(candidate.ExpiresAt):
		return false
	case candidateCanonical != currentCanonical:
		return candidateCanonical
	default:
		return false
	}
}

func normalizeStore(store *AuthStore) {
	if store == nil {
		return
	}
	if store.Credentials == nil {
		store.Credentials = make(map[string]*AuthCredential)
		return
	}

	normalized := make(map[string]*AuthCredential, len(store.Credentials))
	canonicalFlags := make(map[string]bool, len(store.Credentials))

	for provider, cred := range store.Credentials {
		normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
		canonical := canonicalProvider(provider)
		normalizedCred := cloneCredential(cred)
		if normalizedCred != nil {
			normalizedCred.Provider = canonicalProvider(normalizedCred.Provider)
			if normalizedCred.Provider == "" {
				normalizedCred.Provider = canonical
			}
		}

		current := normalized[canonical]
		currentCanonical := canonicalFlags[canonical]
		candidateCanonical := normalizedProvider == canonical

		if shouldPreferCredential(normalizedCred, candidateCanonical, current, currentCanonical) {
			normalized[canonical] = mergeCredentials(normalizedCred, current)
			canonicalFlags[canonical] = candidateCanonical
			continue
		}

		normalized[canonical] = mergeCredentials(current, normalizedCred)
	}

	store.Credentials = normalized
}

func cloneStore(store *AuthStore) *AuthStore {
	if store == nil {
		return &AuthStore{Credentials: make(map[string]*AuthCredential)}
	}
	out := &AuthStore{Credentials: make(map[string]*AuthCredential, len(store.Credentials))}
	for provider, cred := range store.Credentials {
		out.Credentials[provider] = cloneCredential(cred)
	}
	return out
}

func decryptCredentialTokens(provider string, cred *AuthCredential) error {
	if cred == nil {
		return nil
	}
	resolver := credential.NewResolver("")
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "access_token", value: &cred.AccessToken},
		{name: "refresh_token", value: &cred.RefreshToken},
	} {
		if !strings.HasPrefix(*field.value, credential.EncScheme) {
			continue
		}
		plaintext, err := resolver.Resolve(*field.value)
		if err != nil {
			return fmt.Errorf("auth: decrypt %s for %q: %w", field.name, provider, err)
		}
		*field.value = plaintext
	}
	return nil
}

func encryptCredentialTokens(provider string, cred *AuthCredential, passphrase string) error {
	// Without a passphrase keep the legacy plaintext format, mirroring
	// config.SecureString: encryption is opportunistic so existing OAuth
	// logins keep working. Once a passphrase is configured, encryption
	// failures are fatal instead of silently writing plaintext.
	if cred == nil || passphrase == "" {
		return nil
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "access_token", value: &cred.AccessToken},
		{name: "refresh_token", value: &cred.RefreshToken},
	} {
		if *field.value == "" {
			continue
		}
		encrypted, err := credential.Encrypt(passphrase, "", *field.value)
		if err != nil {
			return fmt.Errorf("auth: encrypt %s for %q: %w", field.name, provider, err)
		}
		*field.value = encrypted
	}
	return nil
}

// LoadStore decrypts enc:// OAuth tokens and accepts legacy plaintext stores.
func LoadStore() (*AuthStore, error) {
	path := authFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Credentials: make(map[string]*AuthCredential)}, nil
		}
		return nil, err
	}

	var store AuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	for provider, cred := range store.Credentials {
		if err := decryptCredentialTokens(provider, cred); err != nil {
			return nil, err
		}
	}
	normalizeStore(&store)
	return &store, nil
}

// SaveStore encrypts OAuth tokens with the shared credential key when a
// passphrase is available, without mutating the caller's in-memory store.
// Without key material it keeps the legacy plaintext format and warns, the
// same policy as config.SecureString.
func SaveStore(store *AuthStore) error {
	path := authFilePath()
	encryptedStore := cloneStore(store)
	passphrase := credential.PassphraseProvider()
	if passphrase == "" && storeHasTokens(encryptedStore) {
		logger.WarnCF("auth", "OAuth tokens are stored without encryption; set "+credential.PassphraseEnvVar+" to encrypt auth.json at rest", nil)
	}
	for provider, cred := range encryptedStore.Credentials {
		if err := encryptCredentialTokens(provider, cred, passphrase); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(encryptedStore, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func storeHasTokens(store *AuthStore) bool {
	for _, cred := range store.Credentials {
		if cred != nil && (cred.AccessToken != "" || cred.RefreshToken != "") {
			return true
		}
	}
	return false
}

func GetCredential(provider string) (*AuthCredential, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	cred, ok := store.Credentials[canonicalProvider(provider)]
	if !ok {
		return nil, nil
	}
	return cred, nil
}

func SetCredential(provider string, cred *AuthCredential) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}

	canonical := canonicalProvider(provider)
	normalized := cloneCredential(cred)
	if normalized != nil {
		normalized.Provider = canonicalProvider(normalized.Provider)
		if normalized.Provider == "" {
			normalized.Provider = canonical
		}
	}

	store.Credentials[canonical] = normalized
	return SaveStore(store)
}

func DeleteCredential(provider string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	delete(store.Credentials, canonicalProvider(provider))
	return SaveStore(store)
}

func DeleteAllCredentials() error {
	path := authFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
