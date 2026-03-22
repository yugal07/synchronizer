package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/synchronizer/utils"
)

const resourceRuntimeAlerts = "runtimealerts"

type cachedAccount struct {
	accessKey string
	features  []string
}

// FeaturesProvider checks the features endpoint to determine
// whether a feature flag is enabled for each account. If the feature flag is NOT
// in the enabled features list, alerts from that account should be skipped.
type FeaturesProvider struct {
	featuresURL     string
	featureFlagName string
	client          http.Client
	cache           sync.Map // map[string]*cachedAccount
}

// NewFeaturesProvider creates a provider that checks for feature flags.
func NewFeaturesProvider(featuresURL, featureFlagName string) *FeaturesProvider {
	return &FeaturesProvider{
		featuresURL:     featuresURL,
		featureFlagName: featureFlagName,
		client: http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// RegisterAccount registers an account for feature lookups and fetches its features.
func (p *FeaturesProvider) RegisterAccount(account, accessKey string) {
	if val, ok := p.cache.Load(account); ok {
		existing := val.(*cachedAccount)
		if existing.accessKey == accessKey && existing.features != nil {
			return // already registered and fetched
		}
	}
	p.cache.Store(account, &cachedAccount{accessKey: accessKey})

	go p.refreshAccount(account, accessKey)
}

// UnregisterAccount removes an account from the cache.
func (p *FeaturesProvider) UnregisterAccount(account string) {
	p.cache.Delete(account)
}

// ShouldSkipAlertsFrom returns true if the feature flag is NOT enabled for the account
// (meaning runtime is disabled → skip alerts). Returns false (fail-open) if not cached
// or on any error.
func (p *FeaturesProvider) ShouldSkipAlertsFrom(account string) bool {
	val, ok := p.cache.Load(account)
	if !ok {
		return false // fail-open
	}
	entry := val.(*cachedAccount)
	if entry.features == nil {
		return false // fail-open
	}

	return !slices.Contains(entry.features, p.featureFlagName)
}

// StartRefreshLoop refreshes all registered accounts on a fixed interval.
func (p *FeaturesProvider) StartRefreshLoop(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = time.Hour
	}

	ticker := utils.NewStdTicker(interval)

	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.Chan():
			}

			p.cache.Range(func(key, value any) bool {
				entry := value.(*cachedAccount)
				p.refreshAccount(key.(string), entry.accessKey)
				return true
			})
		}
	}()
}

func (p *FeaturesProvider) refreshAccount(account, accessKey string) {
	features, err := p.fetchFeatures(account, accessKey)
	if err != nil {
		logger.L().Warning("failed to fetch features",
			helpers.String("account", account),
			helpers.Error(err))
		return
	}

	if val, ok := p.cache.Load(account); ok {
		val.(*cachedAccount).features = features
	}
}

type featuresResponse struct {
	Features []string `json:"features"`
}

func (p *FeaturesProvider) fetchFeatures(account, accessKey string) ([]string, error) {
	url := fmt.Sprintf("%s/api/v1/features", p.featuresURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-API-KEY", accessKey)
	req.Header.Set("X-API-ACCOUNT", account)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch features: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("features endpoint returned status %s: %s", resp.Status, string(body))
	}

	var result featuresResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode features response: %w", err)
	}

	return result.Features, nil
}
