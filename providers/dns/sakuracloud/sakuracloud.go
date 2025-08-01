// Package sakuracloud implements a DNS provider for solving the DNS-01 challenge using SakuraCloud DNS.
package sakuracloud

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/platform/config/env"
	"github.com/go-acme/lego/v4/providers/dns/internal/useragent"
	client "github.com/sacloud/api-client-go"
	"github.com/sacloud/iaas-api-go"
	"github.com/sacloud/iaas-api-go/defaults"
	"github.com/sacloud/iaas-api-go/helper/api"
)

// Environment variables names.
const (
	envNamespace = "SAKURACLOUD_"

	EnvAccessToken       = envNamespace + "ACCESS_TOKEN"
	EnvAccessTokenSecret = envNamespace + "ACCESS_TOKEN_SECRET"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
)

var _ challenge.ProviderTimeout = (*DNSProvider)(nil)

// Config is used to configure the creation of the DNSProvider.
type Config struct {
	Token              string
	Secret             string
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	TTL                int
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider.
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, dns01.DefaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, dns01.DefaultPropagationTimeout),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, dns01.DefaultPollingInterval),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 10*time.Second),
		},
	}
}

// DNSProvider implements the challenge.Provider interface.
type DNSProvider struct {
	config *Config
	client iaas.DNSAPI
}

// NewDNSProvider returns a DNSProvider instance configured for SakuraCloud.
// Credentials must be passed in the environment variables:
// SAKURACLOUD_ACCESS_TOKEN & SAKURACLOUD_ACCESS_TOKEN_SECRET.
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvAccessToken, EnvAccessTokenSecret)
	if err != nil {
		return nil, fmt.Errorf("sakuracloud: %w", err)
	}

	config := NewDefaultConfig()
	config.Token = values[EnvAccessToken]
	config.Secret = values[EnvAccessTokenSecret]

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig return a DNSProvider instance configured for SakuraCloud.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("sakuracloud: the configuration of the DNS provider is nil")
	}

	if config.Token == "" {
		return nil, errors.New("sakuracloud: AccessToken is missing")
	}

	if config.Secret == "" {
		return nil, errors.New("sakuracloud: AccessSecret is missing")
	}

	defaultOption, err := api.DefaultOption()
	if err != nil {
		return nil, fmt.Errorf("sakuracloud: %w", err)
	}

	options := &api.CallerOptions{
		Options: &client.Options{
			AccessToken:       config.Token,
			AccessTokenSecret: config.Secret,
			HttpClient:        config.HTTPClient,
			UserAgent:         fmt.Sprintf("%s %s", iaas.DefaultUserAgent, useragent.Get()),
		},
	}

	return &DNSProvider{
		client: iaas.NewDNSOp(newCallerWithOptions(api.MergeOptions(defaultOption, options))),
		config: config,
	}, nil
}

// Present creates a TXT record to fulfill the dns-01 challenge.
func (d *DNSProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)

	err := d.addTXTRecord(info.EffectiveFQDN, info.Value, d.config.TTL)
	if err != nil {
		return fmt.Errorf("sakuracloud: %w", err)
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters.
func (d *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)

	err := d.cleanupTXTRecord(info.EffectiveFQDN, info.Value)
	if err != nil {
		return fmt.Errorf("sakuracloud: %w", err)
	}

	return nil
}

// Timeout returns the timeout and interval to use when checking for DNS propagation.
// Adjusting here to cope with spikes in propagation times.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}

// Extracted from https://github.com/sacloud/iaas-api-go/blob/af06b3ccc2c38625d2dc684ad39590d0ae13eed3/helper/api/caller.go#L36-L81
// Trace and fake are removed.
// Related to https://github.com/sacloud/iaas-api-go/issues/376.
func newCallerWithOptions(opts *api.CallerOptions) iaas.APICaller {
	return newCaller(opts)
}

func newCaller(opts *api.CallerOptions) iaas.APICaller {
	if opts.UserAgent == "" {
		opts.UserAgent = iaas.DefaultUserAgent
	}

	caller := iaas.NewClientWithOptions(opts.Options)

	defaults.DefaultStatePollingTimeout = 72 * time.Hour

	if opts.DefaultZone != "" {
		iaas.APIDefaultZone = opts.DefaultZone
	}

	if len(opts.Zones) > 0 {
		iaas.SakuraCloudZones = opts.Zones
	}

	if opts.APIRootURL != "" {
		if strings.HasSuffix(opts.APIRootURL, "/") {
			opts.APIRootURL = strings.TrimRight(opts.APIRootURL, "/")
		}
		iaas.SakuraCloudAPIRoot = opts.APIRootURL
	}

	return caller
}
