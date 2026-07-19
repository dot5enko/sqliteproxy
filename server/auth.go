package server

import (
	"github.com/go-mysql-org/go-mysql/server"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Username string
	Password string
}

// AuthProvider handles MySQL authentication
type AuthProvider struct {
	config AuthConfig
}

// NewAuthProvider creates a new authentication provider
func NewAuthProvider(config AuthConfig) *AuthProvider {
	return &AuthProvider{
		config: config,
	}
}

// ValidateCredentials checks if the provided credentials are valid
func (a *AuthProvider) ValidateCredentials(username, password string) bool {
	// If no auth configured, allow all connections
	if a.config.Username == "" {
		return true
	}

	// Check username
	if username != a.config.Username {
		return false
	}

	// Check password
	if password != a.config.Password {
		return false
	}

	return true
}

// GetCredentialProvider returns a MySQL credential provider
func (a *AuthProvider) GetCredentialProvider() server.CredentialProvider {
	return &credentialProvider{
		username: a.config.Username,
		password: a.config.Password,
	}
}

// credentialProvider implements server.CredentialProvider
type credentialProvider struct {
	username string
	password string
}

// CheckUsername checks if the username exists
func (cp *credentialProvider) CheckUsername(username string) (bool, error) {
	// If no auth configured, allow all
	if cp.username == "" {
		return true, nil
	}

	if username != cp.username {
		return false, nil
	}

	return true, nil
}

// GetCredential returns the password for the given username
func (cp *credentialProvider) GetCredential(username string) (password string, found bool, err error) {
	// If no auth configured, allow all
	if cp.username == "" {
		return "", true, nil
	}

	if username != cp.username {
		return "", false, nil
	}

	return cp.password, true, nil
}
