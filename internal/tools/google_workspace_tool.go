package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/vault"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleWorkspaceTool handles Google Workspace API actions (Gmail, Drive, Docs).
type GoogleWorkspaceTool struct {
	Vault       *vault.CredentialVault
	SlackClient *slack.SlackClient
	EmployeeID  string
}

var googleWorkspaceScopes = []string{
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/documents",
	"https://www.googleapis.com/auth/drive.file",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/calendar.events",
}

func googleOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes:       googleWorkspaceScopes,
		Endpoint:     google.Endpoint,
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URI"),
	}
}

// getToken retrieves the OAuth2 access token for this employee from the vault.
// If not found, it posts a credential request via Slack and returns an informative error.
func (t *GoogleWorkspaceTool) getToken(ctx context.Context) (string, error) {
	if t.Vault == nil {
		t.postCredentialRequest(ctx)
		return "", errors.New("google_workspace credentials not configured — credential request sent")
	}

	tokenJSON, err := t.Vault.Get(ctx, t.EmployeeID, "google_workspace")
	if err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			t.postCredentialRequest(ctx)
			return "", errors.New("google_workspace credentials not configured — credential request sent")
		}
		return "", fmt.Errorf("google_workspace: vault error: %w", err)
	}

	// Try to unmarshal as an OAuth2 token for refresh support.
	var tok oauth2.Token
	if jsonErr := json.Unmarshal([]byte(tokenJSON), &tok); jsonErr != nil {
		// Treat as a raw access token (backward-compat).
		return tokenJSON, nil
	}

	cfg := googleOAuthConfig()
	ts := cfg.TokenSource(ctx, &tok)
	refreshed, err := ts.Token()
	if err != nil {
		return "", fmt.Errorf("google_workspace: token refresh failed: %w", err)
	}

	// Persist refreshed token back to vault if it changed.
	if refreshed.AccessToken != tok.AccessToken {
		raw, _ := json.Marshal(refreshed)
		_ = t.Vault.Store(ctx, "", t.EmployeeID, "google_workspace", string(raw), "oauth2", googleWorkspaceScopes)
	}

	return refreshed.AccessToken, nil
}

// postCredentialRequest sends a Slack notification requesting Google Workspace credentials.
func (t *GoogleWorkspaceTool) postCredentialRequest(ctx context.Context) {
	if t.SlackClient == nil {
		return
	}
	msg := fmt.Sprintf(
		"[AE] I need Google Workspace access to proceed. "+
			"Please provide OAuth credentials via the /credentials UI at /credentials, "+
			"or authorize via /oauth/google?employee_id=%s",
		t.EmployeeID,
	)
	_, _ = t.SlackClient.PostMessage(ctx, "#general", msg)
}

// Execute dispatches a Google Workspace action.
func (t *GoogleWorkspaceTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	token, err := t.getToken(ctx)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	switch action {
	case "send_email":
		return t.sendEmail(ctx, httpClient, token, input)
	case "list_emails":
		return t.listEmails(ctx, httpClient, token, input)
	case "read_email":
		return t.readEmail(ctx, httpClient, token, input)
	case "create_document":
		return t.createDocument(ctx, httpClient, token, input)
	case "list_drive_files":
		return t.listDriveFiles(ctx, httpClient, token, input)
	case "upload_drive":
		return t.uploadDrive(ctx, httpClient, token, input)
	case "create_event":
		return t.createCalendarEvent(ctx, httpClient, token, input)
	case "list_events":
		return t.listCalendarEvents(ctx, httpClient, token, input)
	case "check_availability":
		return t.checkAvailability(ctx, httpClient, token, input)
	case "delete_event":
		return t.deleteCalendarEvent(ctx, httpClient, token, input)
	case "update_event":
		return t.updateCalendarEvent(ctx, httpClient, token, input)
	default:
		return nil, fmt.Errorf("google_workspace: action %q not implemented", action)
	}
}

// doRequest performs an authenticated HTTP request and returns the response body.
func (t *GoogleWorkspaceTool) doRequest(ctx context.Context, client *http.Client, method, apiURL, token string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, apiURL, body)
	if err != nil {
		return nil, fmt.Errorf("google_workspace: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google_workspace: %s %s: %w", method, apiURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("google_workspace: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("google_workspace: HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// sendEmail sends an email via the Gmail REST API.
func (t *GoogleWorkspaceTool) sendEmail(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	to, _ := input["to"].(string)
	subject, _ := input["subject"].(string)
	body, _ := input["body"].(string)
	if to == "" {
		return nil, errors.New("google_workspace send_email: 'to' is required")
	}

	// Build RFC2822 message and base64url-encode it.
	raw := fmt.Sprintf("To: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", to, subject, body)
	encoded := base64.URLEncoding.EncodeToString([]byte(raw))

	payload, _ := json.Marshal(map[string]string{"raw": encoded})
	data, err := t.doRequest(ctx, client, http.MethodPost,
		"https://gmail.googleapis.com/gmail/v1/users/me/messages/send",
		token, bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}

	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &result)
	return map[string]any{"message_id": result.ID}, nil
}

// listEmails lists emails matching the given query via the Gmail REST API.
func (t *GoogleWorkspaceTool) listEmails(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	query, _ := input["query"].(string)
	maxResults := 10
	if v, ok := input["max_results"].(float64); ok {
		maxResults = int(v)
	}

	params := url.Values{}
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	if query != "" {
		params.Set("q", query)
	}
	apiURL := "https://gmail.googleapis.com/gmail/v1/users/me/messages?" + params.Encode()

	data, err := t.doRequest(ctx, client, http.MethodGet, apiURL, token, nil, "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(data, &result)

	msgs := make([]map[string]any, len(result.Messages))
	for i, m := range result.Messages {
		msgs[i] = map[string]any{"id": m.ID, "thread_id": m.ThreadID}
	}
	return map[string]any{"messages": msgs}, nil
}

// readEmail retrieves the full content of an email via the Gmail REST API.
func (t *GoogleWorkspaceTool) readEmail(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	msgID, _ := input["message_id"].(string)
	if msgID == "" {
		return nil, errors.New("google_workspace read_email: 'message_id' is required")
	}

	apiURL := fmt.Sprintf("https://gmail.googleapis.com/gmail/v1/users/me/messages/%s?format=full", msgID)
	data, err := t.doRequest(ctx, client, http.MethodGet, apiURL, token, nil, "")
	if err != nil {
		return nil, err
	}

	var msg struct {
		Payload struct {
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
			Body struct {
				Data string `json:"data"`
			} `json:"body"`
			Parts []struct {
				MimeType string `json:"mimeType"`
				Body     struct {
					Data string `json:"data"`
				} `json:"body"`
			} `json:"parts"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(data, &msg)

	headers := map[string]string{}
	for _, h := range msg.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "subject", "from", "to", "date":
			headers[strings.ToLower(h.Name)] = h.Value
		}
	}

	// Prefer text/plain part; fall back to top-level body.
	body := ""
	for _, p := range msg.Payload.Parts {
		if p.MimeType == "text/plain" && p.Body.Data != "" {
			decoded, _ := base64.URLEncoding.DecodeString(p.Body.Data)
			body = string(decoded)
			break
		}
	}
	if body == "" && msg.Payload.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(msg.Payload.Body.Data)
		body = string(decoded)
	}

	return map[string]any{
		"subject": headers["subject"],
		"from":    headers["from"],
		"to":      headers["to"],
		"date":    headers["date"],
		"body":    body,
	}, nil
}

// createDocument creates a new Google Doc and inserts content via the Docs REST API.
func (t *GoogleWorkspaceTool) createDocument(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	title, _ := input["title"].(string)
	content, _ := input["content"].(string)
	if title == "" {
		title = "Untitled Document"
	}

	createPayload, _ := json.Marshal(map[string]string{"title": title})
	data, err := t.doRequest(ctx, client, http.MethodPost,
		"https://docs.googleapis.com/v1/documents",
		token, bytes.NewReader(createPayload), "application/json")
	if err != nil {
		return nil, err
	}

	var doc struct {
		DocumentID string `json:"documentId"`
	}
	_ = json.Unmarshal(data, &doc)

	if content != "" {
		updatePayload, _ := json.Marshal(map[string]any{
			"requests": []map[string]any{
				{
					"insertText": map[string]any{
						"location": map[string]any{"index": 1},
						"text":     content,
					},
				},
			},
		})
		batchURL := fmt.Sprintf("https://docs.googleapis.com/v1/documents/%s:batchUpdate", doc.DocumentID)
		_, err = t.doRequest(ctx, client, http.MethodPost, batchURL, token,
			bytes.NewReader(updatePayload), "application/json")
		if err != nil {
			return nil, err
		}
	}

	docURL := fmt.Sprintf("https://docs.google.com/document/d/%s/edit", doc.DocumentID)
	return map[string]any{"document_id": doc.DocumentID, "url": docURL}, nil
}

// listDriveFiles lists files from Google Drive via the Drive REST API.
func (t *GoogleWorkspaceTool) listDriveFiles(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	query, _ := input["query"].(string)
	pageSize := 10
	if v, ok := input["page_size"].(float64); ok {
		pageSize = int(v)
	}

	params := url.Values{}
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("fields", "files(id,name,mimeType,modifiedTime)")
	if query != "" {
		params.Set("q", query)
	}
	apiURL := "https://www.googleapis.com/drive/v3/files?" + params.Encode()

	data, err := t.doRequest(ctx, client, http.MethodGet, apiURL, token, nil, "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Files []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			MimeType     string `json:"mimeType"`
			ModifiedTime string `json:"modifiedTime"`
		} `json:"files"`
	}
	_ = json.Unmarshal(data, &result)

	files := make([]map[string]any, len(result.Files))
	for i, f := range result.Files {
		files[i] = map[string]any{
			"id":            f.ID,
			"name":          f.Name,
			"mime_type":     f.MimeType,
			"modified_time": f.ModifiedTime,
		}
	}
	return map[string]any{"files": files}, nil
}

// uploadDrive uploads a file to Google Drive via multipart upload.
func (t *GoogleWorkspaceTool) uploadDrive(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	name, _ := input["name"].(string)
	content, _ := input["content"].(string)
	mimeType, _ := input["mime_type"].(string)
	if name == "" {
		return nil, errors.New("google_workspace upload_drive: 'name' is required")
	}
	if mimeType == "" {
		mimeType = "text/plain"
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Metadata part.
	metaHeader := textproto.MIMEHeader{}
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, _ := mw.CreatePart(metaHeader)
	metaBytes, _ := json.Marshal(map[string]string{"name": name})
	_, _ = metaPart.Write(metaBytes)

	// Media part.
	mediaHeader := textproto.MIMEHeader{}
	mediaHeader.Set("Content-Type", mimeType)
	mediaPart, _ := mw.CreatePart(mediaHeader)
	_, _ = io.WriteString(mediaPart, content)

	mw.Close()

	apiURL := "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,webViewLink"
	data, err := t.doRequest(ctx, client, http.MethodPost, apiURL, token,
		&buf, "multipart/related; boundary="+mw.Boundary())
	if err != nil {
		return nil, err
	}

	var result struct {
		ID          string `json:"id"`
		WebViewLink string `json:"webViewLink"`
	}
	_ = json.Unmarshal(data, &result)
	return map[string]any{"file_id": result.ID, "url": result.WebViewLink}, nil
}

// period represents a time interval used for free/busy calculations.
type period struct {
	start time.Time
	end   time.Time
}

// createCalendarEvent creates a Google Calendar event via the Calendar REST API.
func (t *GoogleWorkspaceTool) createCalendarEvent(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	title, _ := input["title"].(string)
	description, _ := input["description"].(string)
	start, _ := input["start"].(string)
	end, _ := input["end"].(string)
	location, _ := input["location"].(string)

	if title == "" {
		return nil, errors.New("google_workspace create_event: 'title' is required")
	}
	if start == "" || end == "" {
		return nil, errors.New("google_workspace create_event: 'start' and 'end' are required")
	}

	body := map[string]any{
		"summary":     title,
		"description": description,
		"location":    location,
		"start":       map[string]string{"dateTime": start},
		"end":         map[string]string{"dateTime": end},
	}

	if rawAttendees, ok := input["attendees"]; ok {
		switch v := rawAttendees.(type) {
		case []any:
			attendees := make([]map[string]string, 0, len(v))
			for _, a := range v {
				if email, ok := a.(string); ok && email != "" {
					attendees = append(attendees, map[string]string{"email": email})
				}
			}
			body["attendees"] = attendees
		case []string:
			attendees := make([]map[string]string, 0, len(v))
			for _, email := range v {
				if email != "" {
					attendees = append(attendees, map[string]string{"email": email})
				}
			}
			body["attendees"] = attendees
		}
	}

	payload, _ := json.Marshal(body)
	data, err := t.doRequest(ctx, client, http.MethodPost,
		"https://www.googleapis.com/calendar/v3/calendars/primary/events",
		token, bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}

	var event struct {
		ID       string `json:"id"`
		HTMLLink string `json:"htmlLink"`
		Status   string `json:"status"`
	}
	_ = json.Unmarshal(data, &event)
	return map[string]any{
		"event_id":  event.ID,
		"html_link": event.HTMLLink,
		"status":    event.Status,
	}, nil
}

// listCalendarEvents lists calendar events within a time range.
func (t *GoogleWorkspaceTool) listCalendarEvents(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	timeMin, _ := input["time_min"].(string)
	timeMax, _ := input["time_max"].(string)
	maxResults := 10
	if v, ok := input["max_results"].(float64); ok {
		maxResults = int(v)
	}

	now := time.Now()
	if timeMin == "" {
		timeMin = now.UTC().Format(time.RFC3339)
	}
	if timeMax == "" {
		timeMax = now.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	}

	params := url.Values{}
	params.Set("timeMin", timeMin)
	params.Set("timeMax", timeMax)
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	params.Set("orderBy", "startTime")
	params.Set("singleEvents", "true")

	apiURL := "https://www.googleapis.com/calendar/v3/calendars/primary/events?" + params.Encode()
	data, err := t.doRequest(ctx, client, http.MethodGet, apiURL, token, nil, "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			ID       string `json:"id"`
			Summary  string `json:"summary"`
			HTMLLink string `json:"htmlLink"`
			Start    struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
			Attendees []struct {
				Email string `json:"email"`
			} `json:"attendees"`
		} `json:"items"`
	}
	_ = json.Unmarshal(data, &result)

	events := make([]map[string]any, len(result.Items))
	for i, item := range result.Items {
		start := item.Start.DateTime
		if start == "" {
			start = item.Start.Date
		}
		end := item.End.DateTime
		if end == "" {
			end = item.End.Date
		}
		attendees := make([]string, len(item.Attendees))
		for j, a := range item.Attendees {
			attendees[j] = a.Email
		}
		events[i] = map[string]any{
			"id":        item.ID,
			"summary":   item.Summary,
			"start":     start,
			"end":       end,
			"attendees": attendees,
			"html_link": item.HTMLLink,
		}
	}
	return map[string]any{"events": events}, nil
}

// checkAvailability checks free/busy status for a list of email addresses.
func (t *GoogleWorkspaceTool) checkAvailability(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	timeMin, _ := input["time_min"].(string)
	timeMax, _ := input["time_max"].(string)
	if timeMin == "" || timeMax == "" {
		return nil, errors.New("google_workspace check_availability: 'time_min' and 'time_max' are required")
	}

	var emails []string
	if rawEmails, ok := input["emails"]; ok {
		switch v := rawEmails.(type) {
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok && s != "" {
					emails = append(emails, s)
				}
			}
		case []string:
			emails = v
		}
	}
	if len(emails) == 0 {
		return nil, errors.New("google_workspace check_availability: 'emails' is required")
	}

	items := make([]map[string]string, len(emails))
	for i, e := range emails {
		items[i] = map[string]string{"id": e}
	}

	reqBody, _ := json.Marshal(map[string]any{
		"timeMin": timeMin,
		"timeMax": timeMax,
		"items":   items,
	})
	data, err := t.doRequest(ctx, client, http.MethodPost,
		"https://www.googleapis.com/calendar/v3/freeBusy",
		token, bytes.NewReader(reqBody), "application/json")
	if err != nil {
		return nil, err
	}

	var fbResp struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	_ = json.Unmarshal(data, &fbResp)

	// Collect busy periods per email.
	var allBusy []period
	busyPeriods := make([]map[string]any, 0)

	for _, email := range emails {
		cal, ok := fbResp.Calendars[email]
		if !ok {
			continue
		}
		for _, b := range cal.Busy {
			s, err1 := time.Parse(time.RFC3339, b.Start)
			e, err2 := time.Parse(time.RFC3339, b.End)
			if err1 != nil || err2 != nil {
				continue
			}
			allBusy = append(allBusy, period{s, e})
			busyPeriods = append(busyPeriods, map[string]any{
				"email": email,
				"start": b.Start,
				"end":   b.End,
			})
		}
	}

	// Compute free slots as gaps in merged busy periods within [timeMin, timeMax].
	rangeStart, _ := time.Parse(time.RFC3339, timeMin)
	rangeEnd, _ := time.Parse(time.RFC3339, timeMax)

	// Merge overlapping busy periods.
	merged := mergePeriods(allBusy)

	freeSlots := make([]map[string]any, 0)
	cursor := rangeStart
	for _, p := range merged {
		if p.start.After(cursor) {
			freeSlots = append(freeSlots, map[string]any{
				"start": cursor.UTC().Format(time.RFC3339),
				"end":   p.start.UTC().Format(time.RFC3339),
			})
		}
		if p.end.After(cursor) {
			cursor = p.end
		}
	}
	if cursor.Before(rangeEnd) {
		freeSlots = append(freeSlots, map[string]any{
			"start": cursor.UTC().Format(time.RFC3339),
			"end":   rangeEnd.UTC().Format(time.RFC3339),
		})
	}

	return map[string]any{
		"busy_periods": busyPeriods,
		"free_slots":   freeSlots,
	}, nil
}

// mergePeriods merges overlapping time periods and returns them sorted.
func mergePeriods(periods []period) []period {
	if len(periods) == 0 {
		return nil
	}
	// Sort by start time (insertion sort — list is typically small).
	for i := 1; i < len(periods); i++ {
		for j := i; j > 0 && periods[j].start.Before(periods[j-1].start); j-- {
			periods[j], periods[j-1] = periods[j-1], periods[j]
		}
	}
	merged := []period{periods[0]}
	for _, p := range periods[1:] {
		last := &merged[len(merged)-1]
		if !p.start.After(last.end) {
			if p.end.After(last.end) {
				last.end = p.end
			}
		} else {
			merged = append(merged, p)
		}
	}
	return merged
}

// deleteCalendarEvent deletes a calendar event by ID.
func (t *GoogleWorkspaceTool) deleteCalendarEvent(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	eventID, _ := input["event_id"].(string)
	if eventID == "" {
		return nil, errors.New("google_workspace delete_event: 'event_id' is required")
	}

	apiURL := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/primary/events/%s", eventID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("google_workspace delete_event: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google_workspace delete_event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return map[string]any{"success": true}, nil
	}
	body, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("google_workspace delete_event: HTTP %d: %s", resp.StatusCode, string(body))
}

// updateCalendarEvent patches a calendar event with the provided fields.
func (t *GoogleWorkspaceTool) updateCalendarEvent(ctx context.Context, client *http.Client, token string, input map[string]any) (map[string]any, error) {
	eventID, _ := input["event_id"].(string)
	if eventID == "" {
		return nil, errors.New("google_workspace update_event: 'event_id' is required")
	}

	patch := map[string]any{}
	if title, ok := input["title"].(string); ok && title != "" {
		patch["summary"] = title
	}
	if desc, ok := input["description"].(string); ok && desc != "" {
		patch["description"] = desc
	}
	if start, ok := input["start"].(string); ok && start != "" {
		patch["start"] = map[string]string{"dateTime": start}
	}
	if end, ok := input["end"].(string); ok && end != "" {
		patch["end"] = map[string]string{"dateTime": end}
	}

	payload, _ := json.Marshal(patch)
	apiURL := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/primary/events/%s", eventID)
	data, err := t.doRequest(ctx, client, http.MethodPatch, apiURL, token, bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}

	var event struct {
		ID       string `json:"id"`
		HTMLLink string `json:"htmlLink"`
	}
	_ = json.Unmarshal(data, &event)
	return map[string]any{
		"event_id":  event.ID,
		"html_link": event.HTMLLink,
	}, nil
}
