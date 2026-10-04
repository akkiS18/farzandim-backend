package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// resolveAnnouncementImagePath safely locates an announcement image on the filesystem
// across Windows and Linux, regardless of forward/backward slashes or working directory.
func resolveAnnouncementImagePath(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty image url")
	}

	// 1. If it's an external URL not pointing to local uploads, return empty (handled by URL directly)
	if (strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://")) &&
		!strings.Contains(rawURL, "uploads/announcements/") {
		return "", nil
	}

	// 2. Extract path portion starting with "uploads/announcements/"
	var subPath string
	idx := strings.Index(rawURL, "uploads/announcements/")
	if idx != -1 {
		subPath = rawURL[idx:]
	} else if strings.HasPrefix(rawURL, "/uploads/") || strings.HasPrefix(rawURL, "uploads/") {
		subPath = strings.TrimPrefix(rawURL, "/")
	} else {
		return "", nil
	}

	// 3. Normalize slashes using ToSlash for cross-platform prefix checking
	cleanSlash := filepath.ToSlash(filepath.Clean(subPath))
	if !strings.HasPrefix(cleanSlash, "uploads/announcements/") {
		return "", fmt.Errorf("path outside uploads/announcements: %s", cleanSlash)
	}

	nativeRel := filepath.FromSlash(cleanSlash)

	// 4. Search potential base locations on disk by traversing upwards
	candidates := []string{
		nativeRel,
		filepath.Join(".", nativeRel),
		filepath.Join("backend", nativeRel),
	}

	if cwd, err := os.Getwd(); err == nil {
		cur := cwd
		for i := 0; i < 6; i++ {
			candidates = append(candidates, filepath.Join(cur, nativeRel))
			candidates = append(candidates, filepath.Join(cur, "backend", nativeRel))
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}

	if execPath, err := os.Executable(); err == nil {
		cur := filepath.Dir(execPath)
		for i := 0; i < 6; i++ {
			candidates = append(candidates, filepath.Join(cur, nativeRel))
			candidates = append(candidates, filepath.Join(cur, "backend", nativeRel))
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}

	return "", fmt.Errorf("image not found on disk: %s", cleanSlash)
}

// sendAnnouncementMedia uploads local images directly, so local development and private backends
// do not depend on Telegram servers having direct HTTP access to the backend.
func sendAnnouncementMedia(token string, chatID interface{}, urls []string, text string, buttonArgs ...string) error {
	buttonText := ""
	buttonURL := ""
	if len(buttonArgs) >= 2 {
		buttonText = buttonArgs[0]
		buttonURL = buttonArgs[1]
	}
	return doSendAnnouncementMedia(token, chatID, urls, text, true, buttonText, buttonURL)
}

func sendAnnouncementMediaPlain(token string, chatID interface{}, urls []string, text string, buttonArgs ...string) error {
	buttonText := ""
	buttonURL := ""
	if len(buttonArgs) >= 2 {
		buttonText = buttonArgs[0]
		buttonURL = buttonArgs[1]
	}
	plainText := html.UnescapeString(strings.NewReplacer("<b>", "", "</b>", "").Replace(text))
	return doSendAnnouncementMedia(token, chatID, urls, plainText, false, buttonText, buttonURL)
}

func doSendAnnouncementMedia(token string, chatID interface{}, urls []string, text string, isHTML bool, buttonText, buttonURL string) error {
	if len(urls) == 0 {
		return nil
	}

	caption := ""
	if len(utf16.Encode([]rune(text))) <= 1024 {
		caption = text
	}

	method := "sendPhoto"
	if len(urls) > 1 {
		method = "sendMediaGroup"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", fmt.Sprint(chatID))

	if method == "sendPhoto" {
		url := urls[0]
		if caption != "" {
			_ = writer.WriteField("caption", caption)
			if isHTML {
				_ = writer.WriteField("parse_mode", "HTML")
			}
		}

		if isValidTelegramButtonURL(buttonURL) {
			markup := map[string]interface{}{
				"inline_keyboard": [][]map[string]interface{}{
					{
						{"text": buttonText, "url": buttonURL},
					},
				},
			}
			if markupBytes, err := json.Marshal(markup); err == nil {
				_ = writer.WriteField("reply_markup", string(markupBytes))
			}
		}

		localPath, err := resolveAnnouncementImagePath(url)
		if err != nil {
			return err
		}

		if localPath != "" {
			f, err := os.Open(localPath)
			if err != nil {
				return fmt.Errorf("cannot open image file %s: %w", localPath, err)
			}
			defer f.Close()

			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename="%s"`, filepath.Base(localPath)))
			contentType := "image/jpeg"
			if strings.HasSuffix(strings.ToLower(localPath), ".png") {
				contentType = "image/png"
			}
			h.Set("Content-Type", contentType)

			part, err := writer.CreatePart(h)
			if err != nil {
				return err
			}
			if _, err = io.Copy(part, f); err != nil {
				return err
			}
		} else {
			_ = writer.WriteField("photo", url)
		}
	} else {
		// sendMediaGroup (albums)
		media := make([]map[string]interface{}, 0, len(urls))
		filesToClose := make([]*os.File, 0, len(urls))
		defer func() {
			for _, f := range filesToClose {
				_ = f.Close()
			}
		}()

		for i, url := range urls {
			field := fmt.Sprintf("photo%d", i)
			ref := url

			localPath, err := resolveAnnouncementImagePath(url)
			if err != nil {
				return err
			}

			if localPath != "" {
				f, err := os.Open(localPath)
				if err != nil {
					return fmt.Errorf("cannot open image file %s: %w", localPath, err)
				}
				filesToClose = append(filesToClose, f)

				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filepath.Base(localPath)))
				contentType := "image/jpeg"
				if strings.HasSuffix(strings.ToLower(localPath), ".png") {
					contentType = "image/png"
				}
				h.Set("Content-Type", contentType)

				part, err := writer.CreatePart(h)
				if err != nil {
					return err
				}
				if _, err = io.Copy(part, f); err != nil {
					return err
				}
				ref = "attach://" + field
			}

			item := map[string]interface{}{"type": "photo", "media": ref}
			if i == 0 && caption != "" {
				item["caption"] = caption
				if isHTML {
					item["parse_mode"] = "HTML"
				}
			}
			media = append(media, item)
		}

		encoded, err := json.Marshal(media)
		if err != nil {
			return err
		}
		_ = writer.WriteField("media", string(encoded))
	}

	if err := writer.Close(); err != nil {
		return err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+token+"/"+method, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram media network error: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("invalid telegram media response: %s", string(respBody))
	}
	if !result.OK {
		if isHTML && strings.Contains(strings.ToLower(result.Description), "can't parse entities") {
			return sendAnnouncementMediaPlain(token, chatID, urls, text, buttonText, buttonURL)
		}
		return fmt.Errorf("telegram media error %d: %s", result.ErrorCode, result.Description)
	}
	return nil
}

// Long announcement bodies are split without breaking HTML tags or dropping text.
func (bm *BotManager) sendAnnouncementText(token string, chatID interface{}, text, label, link string) {
	if len(utf16.Encode([]rune(text))) <= 3500 {
		bm.sendTextMessageWithButton(token, chatID, text, label, link)
		return
	}
	plain := html.UnescapeString(strings.NewReplacer("<b>", "", "</b>", "").Replace(text))
	runes := []rune(plain)
	for len(runes) > 0 {
		n := 500
		if len(runes) < n {
			n = len(runes)
		}
		bm.sendTextMessageWithButton(token, chatID, html.EscapeString(string(runes[:n])), label, link)
		runes = runes[n:]
		if len(runes) > 0 {
			time.Sleep(time.Second)
		}
	}
}
