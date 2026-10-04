package services

import (
	"os"
	"testing"
)

func TestResolveAnnouncementImagePath(t *testing.T) {
	// Test existing file
	relPath := "/uploads/announcements/079e9345-b4a7-4fee-bbcd-9c6680195849/e8b2c7b0-06c4-4407-86a8-048329b3eab4.jpg"
	resolved, err := resolveAnnouncementImagePath(relPath)
	if err != nil {
		t.Fatalf("expected to resolve %s, got error: %v", relPath, err)
	}

	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("resolved path %s does not exist: %v", resolved, err)
	}
	t.Logf("Successfully resolved %s -> %s", relPath, resolved)

	// Test with full http localhost URL
	httpURL := "http://localhost:6560/uploads/announcements/079e9345-b4a7-4fee-bbcd-9c6680195849/e8b2c7b0-06c4-4407-86a8-048329b3eab4.jpg"
	resolvedHTTP, err := resolveAnnouncementImagePath(httpURL)
	if err != nil {
		t.Fatalf("expected to resolve %s, got error: %v", httpURL, err)
	}
	if resolvedHTTP != resolved {
		t.Errorf("expected %s, got %s", resolved, resolvedHTTP)
	}

	// Test invalid path outside uploads/announcements
	_, err = resolveAnnouncementImagePath("/uploads/secret/config.json")
	if err == nil {
		t.Errorf("expected error for path outside uploads/announcements")
	}

	// Test non-existent file
	_, err = resolveAnnouncementImagePath("/uploads/announcements/non-existent-uuid/image.jpg")
	if err == nil {
		t.Errorf("expected error for non-existent file")
	}
}
