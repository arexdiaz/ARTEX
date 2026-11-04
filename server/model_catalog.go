package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Autumn-27/artex/agent"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// fetchModelCatalog follows Anthropic's cursor pagination without changing origin
// or forwarding credentials to a server-provided URL. OpenAI/Ollama lists are one page.
func fetchModelCatalog(ctx context.Context, client *http.Client, endpoint string, header http.Header) ([]string, error) {
	models := []string{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for page := 0; page < 100; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		request.Header = header.Clone()
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(body) > 16<<20 {
			return nil, fmt.Errorf("model catalog response is too large")
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("model catalog returned HTTP %d", response.StatusCode)
		}
		var parsed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("invalid model catalog: %w", err)
		}
		for _, model := range parsed.Data {
			if model.ID != "" && !seen[model.ID] {
				models = append(models, model.ID)
				seen[model.ID] = true
			}
		}
		if !parsed.HasMore {
			return models, nil
		}
		if parsed.LastID == "" && len(parsed.Data) > 0 {
			parsed.LastID = parsed.Data[len(parsed.Data)-1].ID
		}
		if parsed.LastID == "" || cursors[parsed.LastID] {
			return nil, fmt.Errorf("invalid model catalog pagination cursor")
		}
		cursors[parsed.LastID] = true
		next, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		query := next.Query()
		query.Set("after_id", parsed.LastID)
		next.RawQuery = query.Encode()
		endpoint = next.String()
	}
	return nil, fmt.Errorf("model catalog exceeded 100 pages")
}

func listProviderModels(ctx context.Context, provider, base, apiKey, proxy string) ([]string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(base), "/")

	// Candidate endpoints to try in order. Some Anthropic-compatible providers
	// (e.g. DeepSeek) implement /v1/messages under an /anthropic path but expose
	// the model list only on their OpenAI-format path — so for anthropic we fall
	// back to the OpenAI endpoint at the stripped root.
	type candidate struct {
		url string
		hdr http.Header
	}
	bearerHdr := func() http.Header {
		h := http.Header{}
		h.Set("Authorization", "Bearer "+apiKey)
		return h
	}
	anthropicHdr := func() http.Header {
		h := http.Header{}
		h.Set("x-api-key", apiKey)
		h.Set("anthropic-version", "2023-06-01")
		return h
	}

	var candidates []candidate
	switch provider {
	case "openai", "openai-responses":
		// Responses API shares the OpenAI model list at /v1/models; tolerate a full
		// endpoint URL of either format.
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		b := strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(baseURL, "/chat/completions"), "/responses"), "/")
		candidates = append(candidates, candidate{b + "/models", bearerHdr()})
	case "ollama":
		// OpenAI-compatible list at {base}/v1/models. Tolerate base URLs with or
		// without the /v1 suffix (and a full endpoint URL). No auth needed; when a
		// key was provided anyway, send it as Bearer (Ollama ignores it).
		if baseURL == "" {
			baseURL = agent.DefaultOllamaBaseURL
		}
		b := strings.TrimSuffix(baseURL, "/chat/completions")
		if !strings.HasSuffix(b, "/v1") {
			b += "/v1"
		}
		hdr := http.Header{}
		if apiKey != "" {
			hdr.Set("Authorization", "Bearer "+apiKey)
		}
		candidates = append(candidates, candidate{b + "/models", hdr})
	default: // anthropic
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		b := strings.TrimRight(strings.TrimSuffix(baseURL, "/v1/messages"), "/")
		candidates = append(candidates, candidate{b + "/v1/models", anthropicHdr()})
		// Fallback for Anthropic-compatible third parties whose model list lives on
		// the OpenAI path: strip a trailing /anthropic and try the OpenAI endpoint.
		if root := strings.TrimRight(strings.TrimSuffix(b, "/anthropic"), "/"); root != b {
			candidates = append(candidates,
				candidate{root + "/models", bearerHdr()},
				candidate{root + "/v1/models", bearerHdr()},
			)
		}
	}

	// Build HTTP client with optional proxy.
	transport := &http.Transport{}
	if p := strings.TrimSpace(proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			transport.Proxy = http.ProxyURL(pu)
		}
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many model catalog redirects")
		}
		if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("model catalog redirect changed origin")
		}
		return nil
	}}
	defer transport.CloseIdleConnections()

	// Try each candidate; return the first that yields a non-empty model list.
	var lastErr string
	emptyOK := false
	for _, c := range candidates {
		models, err := fetchModelCatalog(ctx, client, c.url, c.hdr)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if len(models) > 0 {
			return models, nil
		}
		emptyOK = true // 200 but no model ids — keep trying other candidates
	}
	if emptyOK {
		return []string{}, nil
	}
	if lastErr == "" {
		lastErr = "no model list received"
	}
	return nil, fmt.Errorf("%s", lastErr)
}
