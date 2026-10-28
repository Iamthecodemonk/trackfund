package httpapi

import (
	"errors"
	"io"
	"net/http"
	"trackfund/internal/application/avatar"
	"trackfund/internal/infrastructure/telemetry"
)

func (s *Server) uploadProfileAvatar(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code string) {
		telemetry.Inc("avatar_failures_total")
		writeAPIError(w, status, code, "Could not upload avatar", "")
	}
	if s.app.Avatar == nil {
		fail(503, "avatar_storage_not_configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, avatar.MaxSize+128*1024)
	mr, err := r.MultipartReader()
	if err != nil {
		fail(400, "invalid_multipart")
		return
	}
	part, err := mr.NextPart()
	if err != nil {
		fail(400, "missing_avatar_file")
		return
	}
	if part.FormName() != "file" || part.FileName() == "" {
		fail(400, "invalid_avatar_field")
		return
	}
	body, err := io.ReadAll(io.LimitReader(part, avatar.MaxSize+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(413, "avatar_too_large")
		} else {
			fail(400, "invalid_image")
		}
		return
	}
	if len(body) > avatar.MaxSize {
		fail(413, "avatar_too_large")
		return
	}
	contentType := part.Header.Get("Content-Type")
	_ = part.Close()
	if _, err = mr.NextPart(); err != io.EOF {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(413, "avatar_too_large")
		} else {
			fail(400, "single_avatar_file_required")
		}
		return
	}
	url, err := s.app.Avatar.Upload(r.Context(), currentUser(r).ID, contentType, body)
	if err != nil {
		if input := avatar.IsInputError(err); input != nil {
			fail(input.Status, input.Code)
		} else {
			fail(503, "avatar_upload_failed")
		}
		return
	}
	telemetry.Inc("avatar_uploads_total")
	writeAPISuccess(w, 200, map[string]string{"photoUrl": url})
}
