package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
