// Package devin imports cloud sessions through the HTTP API used by Devin MCP.
// Evidence: live /v3/self and /v3beta1/.../events/details responses, 2026-10-15.
// The beta event endpoint is not in the public v3 OpenAPI specification.
package devin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

var sessionPath = regexp.MustCompile(`^/sessions/((?:devin-)?[A-Za-z0-9_-]+)/?$`)

// IsURL recognizes Devin session links, including malformed paths that Read
// must reject instead of handing the receiving agent a login page.
func IsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Hostname() == "app.devin.ai" || strings.HasSuffix(u.Hostname(), ".devinenterprise.com")) &&
		(u.Path == "/sessions" || strings.HasPrefix(u.Path, "/sessions/"))
}

// Read retrieves a session in timeline order. The supplied client owns the
// transport; credentials only go to api.devin.ai, never the pasted URL.
func Read(ctx context.Context, raw, key string, client *http.Client) (session.Thread, error) {
	t := session.Thread{}
	u, err := url.Parse(raw)
	if err != nil || !IsURL(raw) || u.Scheme != "https" || u.Port() != "" || !sessionPath.MatchString(u.Path) {
		return t, errors.New("devin: paste an HTTPS session URL from app.devin.ai or your Devin Enterprise site")
	}
	if strings.TrimSpace(key) == "" {
		return t, errors.New("devin: set DEVIN_API_KEY to an organization-scoped service-user API key")
	}
	id := strings.TrimPrefix(sessionPath.FindStringSubmatch(u.Path)[1], "devin-")
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	t.Source = session.Source{Ref: session.Ref{Provider: "devin", SessionID: id}, Path: u.String(), Metadata: map[string]string{"url": u.String()}}
	t.Warnings = []string{"Devin shell output may be truncated; non-shell tool failures, attachments and compaction history are unavailable"}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	httpClient := *client
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string, dst any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.devin.ai"+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("devin: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("devin: API returned HTTP %d; check DEVIN_API_KEY and session access", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("devin: decode API response: %w", err)
		}
		return nil
	}
	var self struct {
		OrgID string `json:"org_id"`
	}
	if err := get("/v3/self", &self); err != nil {
		return t, err
	}
	if self.OrgID == "" {
		return t, errors.New("devin: DEVIN_API_KEY must be an organization-scoped service-user key; this key has no org_id")
	}
	commands := make(map[string]string)
	failed := make(map[string]bool)
	var unknown session.UnknownTypes
	for offset := 0; ; offset += 50 {
		var page struct {
			Items []struct {
				Type      string          `json:"event_type"`
				Category  string          `json:"category"`
				CreatedAt int64           `json:"created_at"`
				Contents  json.RawMessage `json:"contents"`
				Truncated []string        `json:"truncated_fields"`
			} `json:"items"`
		}
		path := fmt.Sprintf("/v3beta1/organizations/%s/sessions/devin-%s/events/details?offset=%d&limit=50&max_content_length=100000", url.PathEscape(self.OrgID), id, offset)
		if err := get(path, &page); err != nil {
			return t, err
		}
		if page.Items == nil {
			return t, errors.New("devin: event response has no items; the beta API may have changed")
		}
		for _, e := range page.Items {
			switch e.Type {
			case "initialized", "initial_user_message", "user_message", "devin_message", "shell_process_started", "shell_process_completed", "shell_process_completed_background":
			default:
				if e.Category == "message" {
					unknown.Add(e.Type)
				}
				continue
			}
			var c struct {
				Title, Message, Command string
				ProcessID               string `json:"process_id"`
				ExitCode                string `json:"exit_code"`
				Output                  string `json:"output_trunc"`
			}
			if err := json.Unmarshal(e.Contents, &c); err != nil {
				return t, fmt.Errorf("devin: decode %s: %w", e.Type, err)
			}
			if len(e.Truncated) > 0 {
				t.Warnings = append(t.Warnings, "Devin truncated "+e.Type+" fields: "+strings.Join(e.Truncated, ", "))
			}
			at := time.Unix(e.CreatedAt, 0)
			t.Source.UpdatedAt = at
			switch e.Type {
			case "initialized":
				t.Source.Metadata["title"] = c.Title
			case "initial_user_message", "user_message", "devin_message":
				role := session.RoleUser
				if e.Type == "devin_message" {
					role = session.RoleAssistant
				}
				t.Entries = append(t.Entries, session.Entry{Kind: session.KindMessage, Role: role, Text: c.Message, Time: at})
			case "shell_process_started":
				if c.ProcessID == "" {
					return t, errors.New("devin: shell start has no process_id")
				}
				commands[c.ProcessID] = c.Command
			default:
				if c.ProcessID == "" || c.ExitCode == "" {
					return t, errors.New("devin: shell completion has no process_id or exit_code")
				}
				if c.ExitCode != "" && c.ExitCode != "0" && !failed[c.ProcessID] {
					input, _ := json.Marshal(commands[c.ProcessID])
					t.Entries = append(t.Entries, session.Failure("shell", input, "exit_code="+c.ExitCode+"\n"+c.Output, at))
					failed[c.ProcessID] = true
				}
			}
		}
		if len(page.Items) < 50 {
			break
		}
	}
	t.Warnings = unknown.AppendTo(t.Warnings)
	if len(t.Entries) == 0 {
		return t, errors.New("devin: session has no conversation or failed shell commands to import")
	}
	return t, nil
}
