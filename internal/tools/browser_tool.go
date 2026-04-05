package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"

	"github.com/playwright-community/playwright-go"
)

var (
	playwrightOnce sync.Once
	playwrightErr  error
)

func ensurePlaywright() error {
	playwrightOnce.Do(func() {
		playwrightErr = playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}})
	})
	return playwrightErr
}

// BrowserTool provides headless browser automation actions.
type BrowserTool struct{}

// browserStep executes a single interact_sequence step on an already-loaded page.
func browserStep(page playwright.Page, timeout float64, action, selector, value string) error {
	switch action {
	case "click":
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("wait for selector %q: %w", selector, err)
		}
		return page.Locator(selector).Click()
	case "fill":
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("wait for selector %q: %w", selector, err)
		}
		return page.Locator(selector).Fill(value)
	case "type":
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("wait for selector %q: %w", selector, err)
		}
		return page.Locator(selector).Type(value)
	case "select":
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("wait for selector %q: %w", selector, err)
		}
		_, err := page.Locator(selector).SelectOption(playwright.SelectOptionValues{Values: playwright.StringSlice(value)})
		return err
	default:
		return fmt.Errorf("unknown step action %q", action)
	}
}

// Execute dispatches a browser action.
// Supported actions: navigate, extract_text, screenshot, fetch_html,
// click, fill_form, type_text, select_option, submit_form, interact_sequence, print_pdf.
func (t *BrowserTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	if err := ensurePlaywright(); err != nil {
		return nil, fmt.Errorf("browser: playwright install: %w", err)
	}

	// print_pdf accepts either html content or a url; handled separately.
	if action == "print_pdf" {
		return t.printPDF(ctx, input)
	}

	url, _ := input["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("browser.%s: url required", action)
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("browser: start playwright: %w", err)
	}
	defer pw.Stop() //nolint:errcheck

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args:     []string{"--no-sandbox", "--disable-setuid-sandbox"},
	})
	if err != nil {
		return nil, fmt.Errorf("browser: launch chromium: %w", err)
	}
	defer browser.Close()

	timeout := float64(30_000) // 30s in ms
	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("browser: new page: %w", err)
	}

	resp, err := page.Goto(url, playwright.PageGotoOptions{
		Timeout:   &timeout,
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	})
	if err != nil {
		return nil, fmt.Errorf("browser.%s: navigate to %s: %w", action, url, err)
	}

	switch action {
	case "navigate":
		title, err := page.Title()
		if err != nil {
			return nil, fmt.Errorf("browser.navigate: get title: %w", err)
		}
		status := 0
		if resp != nil {
			status = resp.Status()
		}
		return map[string]any{
			"title":  title,
			"url":    page.URL(),
			"status": status,
		}, nil

	case "extract_text":
		selector, _ := input["selector"].(string)
		if selector == "" {
			selector = "body"
		}
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.extract_text: wait for selector %q: %w", selector, err)
		}
		el, err := page.QuerySelector(selector)
		if err != nil {
			return nil, fmt.Errorf("browser.extract_text: query selector %q: %w", selector, err)
		}
		if el == nil {
			return nil, fmt.Errorf("browser.extract_text: selector %q not found", selector)
		}
		text, err := el.InnerText()
		if err != nil {
			return nil, fmt.Errorf("browser.extract_text: inner text: %w", err)
		}
		return map[string]any{"text": text}, nil

	case "screenshot":
		selector, _ := input["selector"].(string)
		var imgBytes []byte

		if selector != "" {
			if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
				return nil, fmt.Errorf("browser.screenshot: wait for selector %q: %w", selector, err)
			}
			el, err := page.QuerySelector(selector)
			if err != nil {
				return nil, fmt.Errorf("browser.screenshot: query selector %q: %w", selector, err)
			}
			if el == nil {
				return nil, fmt.Errorf("browser.screenshot: selector %q not found", selector)
			}
			imgBytes, err = el.Screenshot()
			if err != nil {
				return nil, fmt.Errorf("browser.screenshot: element screenshot: %w", err)
			}
		} else {
			fullPage := true
			imgBytes, err = page.Screenshot(playwright.PageScreenshotOptions{FullPage: &fullPage})
			if err != nil {
				return nil, fmt.Errorf("browser.screenshot: full-page screenshot: %w", err)
			}
		}

		return map[string]any{
			"base64":    base64.StdEncoding.EncodeToString(imgBytes),
			"mime_type": "image/png",
		}, nil

	case "fetch_html":
		html, err := page.Content()
		if err != nil {
			return nil, fmt.Errorf("browser.fetch_html: get content: %w", err)
		}
		return map[string]any{"html": html}, nil

	case "click":
		selector, _ := input["selector"].(string)
		if selector == "" {
			return nil, fmt.Errorf("browser.click: selector required")
		}
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.click: wait for selector %q: %w", selector, err)
		}
		if err := page.Locator(selector).Click(); err != nil {
			return nil, fmt.Errorf("browser.click: click %q: %w", selector, err)
		}
		return map[string]any{"success": true, "final_url": page.URL()}, nil

	case "fill_form":
		fieldsRaw, _ := input["fields"].([]interface{})
		if fieldsRaw == nil {
			return nil, fmt.Errorf("browser.fill_form: fields required")
		}
		for _, f := range fieldsRaw {
			field, _ := f.(map[string]interface{})
			selector, _ := field["selector"].(string)
			value, _ := field["value"].(string)
			if selector == "" {
				return nil, fmt.Errorf("browser.fill_form: field missing selector")
			}
			if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
				return nil, fmt.Errorf("browser.fill_form: wait for selector %q: %w", selector, err)
			}
			if err := page.Locator(selector).Fill(value); err != nil {
				return nil, fmt.Errorf("browser.fill_form: fill %q: %w", selector, err)
			}
		}
		return map[string]any{"success": true, "final_url": page.URL()}, nil

	case "type_text":
		selector, _ := input["selector"].(string)
		text, _ := input["text"].(string)
		if selector == "" {
			return nil, fmt.Errorf("browser.type_text: selector required")
		}
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.type_text: wait for selector %q: %w", selector, err)
		}
		if err := page.Locator(selector).Type(text); err != nil {
			return nil, fmt.Errorf("browser.type_text: type into %q: %w", selector, err)
		}
		return map[string]any{"success": true}, nil

	case "select_option":
		selector, _ := input["selector"].(string)
		value, _ := input["value"].(string)
		if selector == "" {
			return nil, fmt.Errorf("browser.select_option: selector required")
		}
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.select_option: wait for selector %q: %w", selector, err)
		}
		if _, err := page.Locator(selector).SelectOption(playwright.SelectOptionValues{Values: playwright.StringSlice(value)}); err != nil {
			return nil, fmt.Errorf("browser.select_option: select %q in %q: %w", value, selector, err)
		}
		return map[string]any{"success": true}, nil

	case "submit_form":
		selector, _ := input["selector"].(string)
		if selector == "" {
			selector = "form"
		}
		if _, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.submit_form: wait for selector %q: %w", selector, err)
		}
		if _, err := page.Locator(selector).Evaluate("form => form.submit()", nil); err != nil {
			return nil, fmt.Errorf("browser.submit_form: submit: %w", err)
		}
		if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{Timeout: &timeout}); err != nil {
			return nil, fmt.Errorf("browser.submit_form: wait for load: %w", err)
		}
		return map[string]any{"success": true, "final_url": page.URL()}, nil

	case "interact_sequence":
		stepsRaw, _ := input["steps"].([]interface{})
		if stepsRaw == nil {
			return nil, fmt.Errorf("browser.interact_sequence: steps required")
		}
		for i, s := range stepsRaw {
			step, _ := s.(map[string]interface{})
			stepAction, _ := step["action"].(string)
			stepSelector, _ := step["selector"].(string)
			stepValue, _ := step["value"].(string)
			if err := browserStep(page, timeout, stepAction, stepSelector, stepValue); err != nil {
				return nil, fmt.Errorf("browser.interact_sequence: step %d (%s): %w", i, stepAction, err)
			}
		}
		return map[string]any{"success": true, "final_url": page.URL()}, nil

	default:
		return nil, fmt.Errorf("browser: unknown action %q", action)
	}
}

// printPDF renders HTML to PDF using Playwright. Accepts input["html"] or input["url"].
func (t *BrowserTool) printPDF(ctx context.Context, input map[string]any) (map[string]any, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("browser.print_pdf: start playwright: %w", err)
	}
	defer pw.Stop() //nolint:errcheck

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args:     []string{"--no-sandbox", "--disable-setuid-sandbox"},
	})
	if err != nil {
		return nil, fmt.Errorf("browser.print_pdf: launch chromium: %w", err)
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("browser.print_pdf: new page: %w", err)
	}

	timeout := float64(30_000)

	navigateURL, _ := input["url"].(string)

	// If html is provided, write to a temp file and navigate to it.
	if htmlContent, ok := input["html"].(string); ok && htmlContent != "" {
		tmpFile, err := os.CreateTemp("", "invoice-*.html")
		if err != nil {
			return nil, fmt.Errorf("browser.print_pdf: create temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.WriteString(htmlContent); err != nil {
			tmpFile.Close()
			return nil, fmt.Errorf("browser.print_pdf: write temp file: %w", err)
		}
		tmpFile.Close()
		navigateURL = "file://" + tmpFile.Name()
	}

	if navigateURL == "" {
		return nil, fmt.Errorf("browser.print_pdf: html or url required")
	}

	if _, err := page.Goto(navigateURL, playwright.PageGotoOptions{
		Timeout:   &timeout,
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	}); err != nil {
		return nil, fmt.Errorf("browser.print_pdf: navigate: %w", err)
	}

	pdfBytes, err := page.PDF(playwright.PagePdfOptions{
		PrintBackground: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("browser.print_pdf: generate pdf: %w", err)
	}

	return map[string]any{
		"pdf_base64": base64.StdEncoding.EncodeToString(pdfBytes),
	}, nil
}
