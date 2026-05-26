package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const homeAssistantCommandTimeout = 8 * time.Second

type homeAssistantClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type homeAssistantState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func newHomeAssistantClientFromEnv() (*homeAssistantClient, error) {
	baseURL := strings.TrimRight(envAny("HA_BASE_URL", "HOME_ASSISTANT_BASE_URL"), "/")
	token := envAny("HA_ACCESS_TOKEN", "HOME_ASSISTANT_TOKEN")
	if baseURL == "" && token == "" {
		return nil, nil
	}
	if baseURL == "" {
		return nil, fmt.Errorf("HA_BASE_URL is required when Home Assistant control is configured")
	}
	if token == "" {
		return nil, fmt.Errorf("HA_ACCESS_TOKEN is required when Home Assistant control is configured")
	}
	return &homeAssistantClient{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: homeAssistantCommandTimeout},
	}, nil
}

func (c *homeAssistantClient) SetState(ctx context.Context, entityID, state string) error {
	domain := entityDomain(entityID)
	if domain == "" {
		return fmt.Errorf("entity_id %q must include a Home Assistant domain", entityID)
	}
	service := "turn_off"
	if state == "on" {
		service = "turn_on"
	}
	return c.post(ctx, "/api/services/"+domain+"/"+service, map[string]string{
		"entity_id": entityID,
	})
}

func (c *homeAssistantClient) SetBrightnessPercent(ctx context.Context, entityID string, percent int) error {
	if entityDomain(entityID) != "light" {
		return fmt.Errorf("brightness is only supported for light entities, got %q", entityID)
	}
	if percent < 1 || percent > 100 {
		return fmt.Errorf("brightness percent must be between 1 and 100, got %d", percent)
	}
	return c.post(ctx, "/api/services/light/turn_on", map[string]any{
		"entity_id":      entityID,
		"brightness_pct": percent,
	})
}

func (c *homeAssistantClient) GetState(ctx context.Context, entityID string) (homeAssistantState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/states/"+entityID, nil)
	if err != nil {
		return homeAssistantState{}, err
	}
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return homeAssistantState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return homeAssistantState{}, fmt.Errorf("home assistant returned HTTP %d", resp.StatusCode)
	}
	var payload homeAssistantState
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return homeAssistantState{}, err
	}
	return payload, nil
}

func (c *homeAssistantClient) ListStates(ctx context.Context) ([]homeAssistantState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/states", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("home assistant returned HTTP %d", resp.StatusCode)
	}
	var payload []homeAssistantState
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func newHomeAssistantClient(baseURL, token string) (*homeAssistantClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	return &homeAssistantClient{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: homeAssistantCommandTimeout},
	}, nil
}

func (c *homeAssistantClient) post(ctx context.Context, path string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("home assistant returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *homeAssistantClient) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
}

func entityDomain(entityID string) string {
	parts := strings.SplitN(strings.TrimSpace(entityID), ".", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}
