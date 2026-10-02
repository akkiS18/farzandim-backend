package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// sendAnnouncementMedia uploads local images directly, so local storage does not
// depend on Telegram being able to reach a private backend URL.
func sendAnnouncementMedia(token string, chatID interface{}, urls []string, text string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", fmt.Sprint(chatID))
	method := "sendMediaGroup"
	if len(urls) == 1 {
		method = "sendPhoto"
	}
	media := make([]map[string]string, 0, len(urls))
	for i, url := range urls {
		field := fmt.Sprintf("photo%d", i)
		ref := url
		if strings.HasPrefix(url, "/uploads/announcements/") {
			clean := filepath.Clean(strings.TrimPrefix(url, "/"))
			if !strings.HasPrefix(clean, "uploads/announcements/") {
				return fmt.Errorf("invalid image path")
			}
			f, err := os.Open(clean)
			if err != nil {
				return fmt.Errorf("image unavailable")
			}
			part, err := writer.CreateFormFile(field, filepath.Base(clean))
			if err != nil {
				f.Close()
				return err
			}
			_, err = io.Copy(part, f)
			f.Close()
			if err != nil {
				return err
			}
			ref = "attach://" + field
		}
		item := map[string]string{"type": "photo", "media": ref}
		if i == 0 && len(utf16.Encode([]rune(text))) <= 1024 {
			item["caption"] = text
			item["parse_mode"] = "HTML"
		}
		if len(urls) == 1 {
			_ = writer.WriteField("photo", ref)
			if item["caption"] != "" {
				_ = writer.WriteField("caption", text)
				_ = writer.WriteField("parse_mode", "HTML")
			}
		}
		media = append(media, item)
	}
	if len(urls) > 1 {
		encoded, _ := json.Marshal(media)
		_ = writer.WriteField("media", string(encoded))
	}
	if err := writer.Close(); err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post("https://api.telegram.org/bot"+token+"/"+method, writer.FormDataContentType(), &body)
	if err != nil {
		return fmt.Errorf("Telegram media network error")
	}
	defer resp.Body.Close()
	var result struct {
		OK        bool `json:"ok"`
		ErrorCode int  `json:"error_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid Telegram media response")
	}
	if !result.OK {
		return fmt.Errorf("Telegram media error %d", result.ErrorCode)
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
