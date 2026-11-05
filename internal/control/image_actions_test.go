package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"omb/internal/images"
)

func TestImagesPageDeletionInLastColumn(t *testing.T) {
	data := pageData{Info: requestContext{User: User{Role: "admin"}, CSRF: "test"}, ISOImages: []imageRow{{Image: images.Image{StorageID: 7, Path: "Windows/win.iso", Present: true}}}}
	res := httptest.NewRecorder()
	render(res, "images", data)
	body := res.Body.String()
	start := strings.Index(body, `<td class="iso-actions">`)
	if start < 0 || !strings.Contains(body[start:], `action="/images/delete"`) || !strings.Contains(body[start:], `popovertarget=`) || !strings.Contains(body[start:], `Подтвердить удаление ISO`) {
		t.Fatal("ISO deletion must appear in the final action cell with a confirmation")
	}
}
