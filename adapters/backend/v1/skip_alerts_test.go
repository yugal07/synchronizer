package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestFeaturesServer(t *testing.T, features []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/features", r.URL.Path)
		assert.NotEmpty(t, r.Header.Get("X-API-KEY"))
		assert.NotEmpty(t, r.Header.Get("X-API-ACCOUNT"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(featuresResponse{Features: features})
	}))
}

func TestFeaturesProvider_FeatureEnabled(t *testing.T) {
	server := newTestFeaturesServer(t, []string{"threat-detection", "compliance"})
	defer server.Close()

	provider := NewFeaturesProvider(server.URL, "threat-detection")
	provider.RegisterAccount("account-1", "key-1")

	assert.Eventually(t, func() bool {
		return !provider.ShouldSkipAlertsFrom("account-1")
	}, 5*time.Second, 50*time.Millisecond,
		"should not skip when threat-detection is enabled")
}

func TestFeaturesProvider_FeatureDisabled(t *testing.T) {
	server := newTestFeaturesServer(t, []string{"compliance", "workflows"})
	defer server.Close()

	provider := NewFeaturesProvider(server.URL, "threat-detection")
	provider.RegisterAccount("account-1", "key-1")

	assert.Eventually(t, func() bool {
		return provider.ShouldSkipAlertsFrom("account-1")
	}, 5*time.Second, 50*time.Millisecond,
		"should skip when threat-detection is not enabled")
}

func TestFeaturesProvider_NotRegistered(t *testing.T) {
	provider := NewFeaturesProvider("http://unused", "threat-detection")
	assert.False(t, provider.ShouldSkipAlertsFrom("unknown-account"),
		"should not skip for unregistered account (fail-open)")
}

func TestFeaturesProvider_DashboardUnavailable(t *testing.T) {
	requestReceived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestReceived <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": "internal error"}`)
	}))
	defer server.Close()

	provider := NewFeaturesProvider(server.URL, "threat-detection")
	provider.RegisterAccount("account-1", "key-1")

	<-requestReceived

	assert.False(t, provider.ShouldSkipAlertsFrom("account-1"),
		"should not skip when features endpoint is unavailable (fail-open)")
}

func TestFeaturesProvider_HeadersPassedCorrectly(t *testing.T) {
	var mu sync.Mutex
	var receivedKey, receivedAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedKey = r.Header.Get("X-API-KEY")
		receivedAccount = r.Header.Get("X-API-ACCOUNT")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(featuresResponse{Features: []string{"threat-detection"}})
	}))
	defer server.Close()

	provider := NewFeaturesProvider(server.URL, "threat-detection")
	provider.RegisterAccount("my-account", "my-secret-key")

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return receivedKey == "my-secret-key" && receivedAccount == "my-account"
	}, 5*time.Second, 50*time.Millisecond, "headers should be passed correctly")
}

func TestFeaturesProvider_Unregister(t *testing.T) {
	server := newTestFeaturesServer(t, []string{"compliance"})
	defer server.Close()

	provider := NewFeaturesProvider(server.URL, "threat-detection")
	provider.RegisterAccount("account-1", "key-1")

	assert.Eventually(t, func() bool {
		return provider.ShouldSkipAlertsFrom("account-1")
	}, 5*time.Second, 50*time.Millisecond, "should skip initially")

	provider.UnregisterAccount("account-1")

	assert.False(t, provider.ShouldSkipAlertsFrom("account-1"),
		"should not skip after unregister (fail-open)")
}
