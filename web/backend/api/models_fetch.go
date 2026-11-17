package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

type upstreamModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

func fetchUpstreamModels(ctx context.Context, provider, apiBase, apiKey string) ([]upstreamModel, error) {
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	provider = providers.NormalizeProvider(provider)
	allowPrivate := hasLocalAPIBase(apiBase)

	var fetchURL string
	switch provider {
	case "ollama":
		// Strip /v1 suffix if present to get the Ollama root
		root := apiBase
		if strings.HasSuffix(root, "/v1") {
			root = root[:len(root)-3]
		}
		root = strings.TrimRight(root, "/")
		fetchURL = root + "/api/tags"
		return fetchOllamaModels(ctx, fetchURL, allowPrivate)
	case "nearai":
		fetchURL = apiBase + "/model/list"
		return fetchNearAIModels(ctx, fetchURL, apiKey, allowPrivate)
	default:
		// OpenAI-compatible: /v1/models
		fetchURL = apiBase + "/models"
		return fetchOpenAICompatibleModels(ctx, fetchURL, apiKey, allowPrivate)
	}
}

func newModelHTTPClient(allowPrivate bool) (*http.Client, error) {
	return utils.CreateSafeHTTPClient(utils.SafeHTTPClientOptions{
		Timeout: 10 * time.Second,
		AllowPrivateHosts: func() bool {
			return allowPrivate
		},
		MaxRedirects: 5,
	})
}

func readModelResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxModelResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxModelResponseBytes {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", maxModelResponseBytes)
	}
	return data, nil
}

func fetchNearAIModels(ctx context.Context, fetchURL, apiKey string, allowPrivate bool) ([]upstreamModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client, err := newModelHTTPClient(allowPrivate)
	if err != nil {
		return nil, err
	}
	utils.AllowConfiguredProxyFirstHop(req, client.Transport)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nearai returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Models []struct {
			ModelID  string `json:"modelId"`
			OwnedBy  string `json:"ownedBy"`
			Metadata struct {
				OwnedBy string `json:"ownedBy"`
			} `json:"metadata"`
		} `json:"models"`
	}
	body, err := readModelResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	models := make([]upstreamModel, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		id := strings.TrimSpace(m.ModelID)
		if id == "" {
			continue
		}
		ownedBy := strings.TrimSpace(m.OwnedBy)
		if ownedBy == "" {
			ownedBy = strings.TrimSpace(m.Metadata.OwnedBy)
		}
		models = append(models, upstreamModel{ID: id, OwnedBy: ownedBy})
	}
	return models, nil
}

func fetchOpenAICompatibleModels(ctx context.Context, fetchURL, apiKey string, allowPrivate bool) ([]upstreamModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client, err := newModelHTTPClient(allowPrivate)
	if err != nil {
		return nil, err
	}
	utils.AllowConfiguredProxyFirstHop(req, client.Transport)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	body, err := readModelResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	type modelItem struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}

	// {"data": [...]} envelope. Distinguish "envelope shape with empty list"
	// from "object without a data key" via Data being non-nil after unmarshal:
	// json.Unmarshal sets Data to []modelItem{} for `{"data":[]}` but leaves
	// it as nil when "data" is absent or null.
	var envelope struct {
		Data []modelItem `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Data != nil {
		models := make([]upstreamModel, 0, len(envelope.Data))
		for _, m := range envelope.Data {
			if m.ID != "" {
				models = append(models, upstreamModel{ID: m.ID, OwnedBy: m.OwnedBy})
			}
		}
		return models, nil
	}

	// Bare-array shape, including `[]`.
	var arr []modelItem
	if err := json.Unmarshal(body, &arr); err == nil {
		models := make([]upstreamModel, 0, len(arr))
		for _, m := range arr {
			if m.ID != "" {
				models = append(models, upstreamModel{ID: m.ID, OwnedBy: m.OwnedBy})
			}
		}
		return models, nil
	}

	preview := body
	if len(preview) > 256 {
		preview = preview[:256]
	}
	return nil, fmt.Errorf("decode response: unrecognized shape: %s", strings.TrimSpace(string(preview)))
}

func fetchOllamaModels(ctx context.Context, fetchURL string, allowPrivate bool) ([]upstreamModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
	}

	client, err := newModelHTTPClient(allowPrivate)
	if err != nil {
		return nil, err
	}
	utils.AllowConfiguredProxyFirstHop(req, client.Transport)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	body, err := readModelResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	models := make([]upstreamModel, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		id := m.Name
		if id == "" {
			id = m.Model
		}
		if id != "" {
			models = append(models, upstreamModel{ID: id})
		}
	}
	return models, nil
}

// normalizeAPIBaseForCompare normalizes an API base URL for equality comparison
// by trimming trailing slashes and lowering the scheme/host.
func normalizeAPIBaseForCompare(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	if u.Host == "" {
		u, err = url.Parse("//" + raw)
		if err != nil {
			return strings.ToLower(raw)
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
}

// handleTestModel tests connectivity to a model endpoint.
//
//	POST /api/models/{index}/test
