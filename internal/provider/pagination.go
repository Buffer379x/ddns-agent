package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// listPaged follows the pagination formats used by Linode, DigitalOcean,
// Name.com and Vultr. Never interpret an incomplete list as a missing record.
func listPaged[T any](ctx context.Context, client *http.Client, initial *http.Request, collection string) ([]T, error) {
	current := *initial.URL
	seen := map[string]bool{}
	result := []T{}
	for page := 1; page <= 100; page++ {
		if seen[current.String()] {
			return nil, errors.New("provider repeated a pagination cursor")
		}
		seen[current.String()] = true
		req := initial.Clone(ctx)
		req.URL = &current
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var body map[string]json.RawMessage
		err = decodeResponse(res, &body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		var items []T
		if raw, ok := body[collection]; !ok {
			return nil, fmt.Errorf("provider response is missing %s", collection)
		} else if err = json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(result) > 20000 {
			return nil, errors.New("provider record list exceeds 20000 entries")
		}
		var meta struct {
			Pages    int `json:"pages"`
			NextPage int `json:"nextPage"`
			Links    struct {
				Pages struct {
					Next string `json:"next"`
				} `json:"pages"`
			} `json:"links"`
			Meta struct {
				Links struct {
					Next string `json:"next"`
				} `json:"links"`
			} `json:"meta"`
		}
		encoded, _ := json.Marshal(body)
		if err = json.Unmarshal(encoded, &meta); err != nil {
			return nil, err
		}
		// Vultr returns an opaque cursor, while DigitalOcean returns a URL.
		if cursor := meta.Meta.Links.Next; cursor != "" {
			query := current.Query()
			query.Set("cursor", cursor)
			current.RawQuery = query.Encode()
			continue
		}
		next := meta.Links.Pages.Next
		if next != "" {
			parsed, err := url.Parse(next)
			if err != nil {
				return nil, errors.New("invalid pagination URL")
			}
			resolved := current.ResolveReference(parsed)
			if resolved.Scheme != initial.URL.Scheme || resolved.Host != initial.URL.Host || resolved.Path != initial.URL.Path || resolved.User != nil {
				return nil, errors.New("unsafe pagination URL")
			}
			current = *resolved
		} else if meta.NextPage > page || meta.Pages > page {
			nextPage := page + 1
			if meta.NextPage > page {
				nextPage = meta.NextPage
			}
			query := current.Query()
			query.Set("page", strconv.Itoa(nextPage))
			current.RawQuery = query.Encode()
		} else {
			return result, nil
		}
	}
	return nil, errors.New("provider pagination exceeds 100 pages")
}
