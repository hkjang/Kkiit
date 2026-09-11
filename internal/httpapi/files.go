package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func (s *Server) uploadOrderFile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	orderID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if _, _, _, allowed := s.orderAccess(r, orderID, p); !allowed {
		writeError(w, 403, "order_access_denied", "이 주문에 파일을 올릴 수 없습니다.")
		return
	}
	s.storeUpload(w, r, &orderID, "private")
}

// uploadMyFile accepts media a seller shows in public, such as portfolio work.
// It is stored with an explicit public marker so the download handler can serve
// it to visitors without widening access to order attachments.
func (s *Server) uploadMyFile(w http.ResponseWriter, r *http.Request) {
	s.storeUpload(w, r, nil, "public")
}

func (s *Server) storeUpload(w http.ResponseWriter, r *http.Request, orderID *uuid.UUID, visibility string) {
	p, _ := principalFrom(r.Context())
	policy, err := s.settingObject(r, "storage.policy")
	if err != nil {
		writeError(w, 503, "storage_unavailable", "파일 저장 정책을 확인하지 못했습니다.")
		return
	}
	driver, _ := policy["driver"].(string)
	if driver != "database" {
		writeError(w, 501, "storage_adapter_required", "설정한 Storage Adapter가 아직 연결되지 않았습니다.")
		return
	}
	maxMB := int64(50)
	if value, ok := policy["max_upload_mb"].(float64); ok && value >= 1 && value <= 1024 {
		maxMB = int64(value)
	}
	maxBytes := maxMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024*1024)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeError(w, 413, "file_too_large", fmt.Sprintf("파일은 최대 %dMB까지 업로드할 수 있습니다.", maxMB))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file_required", "file 필드에 파일을 첨부해 주세요.")
		return
	}
	defer file.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		writeError(w, 413, "file_too_large", fmt.Sprintf("파일은 최대 %dMB까지 업로드할 수 있습니다.", maxMB))
		return
	}
	if len(data) == 0 {
		writeError(w, 400, "empty_file", "빈 파일은 업로드할 수 없습니다.")
		return
	}
	detected := http.DetectContentType(data)
	if detected == "application/x-msdownload" || detected == "application/x-dosexec" || bytes.HasPrefix(data, []byte{'M', 'Z'}) || bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		writeError(w, 400, "unsafe_file_type", "실행 파일은 업로드할 수 없습니다.")
		return
	}
	if raw, ok := policy["allowed_mime_types"].([]any); ok && len(raw) > 0 {
		mimeAllowed := false
		for _, item := range raw {
			if value, ok := item.(string); ok && value == detected {
				mimeAllowed = true
				break
			}
		}
		if !mimeAllowed {
			writeError(w, 400, "mime_not_allowed", "허용되지 않은 파일 형식입니다.")
			return
		}
	}
	// Size alone is not a limit: without a count and a daily volume ceiling a
	// single participant can fill the database with legitimate sized files.
	if message, ok := s.withinUploadQuota(r, policy, orderID, int64(len(data))); !ok {
		writeError(w, 429, "upload_quota_exceeded", message)
		return
	}
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if name == "." || name == "" {
		name = "upload"
	}
	fileID := uuid.New()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	storageKey := "users/" + p.UserID.String() + "/" + fileID.String()
	if orderID != nil {
		storageKey = "orders/" + orderID.String() + "/" + fileID.String()
	}
	scanState := "clean"
	if security, err := s.settingObject(r, "security.policy"); err == nil {
		if required, _ := security["malware_scan_required"].(bool); required {
			scanState = "pending"
		}
	}
	_, err = s.DB.Exec(r.Context(), `INSERT INTO file_objects(id,owner_id,order_id,storage_key,original_name,mime_type,size_bytes,sha256,scan_state,storage_driver,data,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'database',$10,$11)`,
		fileID, p.UserID, orderID, storageKey, name, detected, len(data), digest, scanState, data, map[string]any{"visibility": visibility})
	if err != nil {
		writeError(w, 500, "upload_failed", "파일을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "file.upload", "file", fileID.String(), nil, map[string]any{"order_id": orderID, "name": name, "size": len(data), "sha256": digest, "visibility": visibility}, "success")
	writeJSON(w, 201, map[string]any{"id": fileID, "name": name, "mime_type": detected, "size_bytes": len(data), "sha256": digest, "scan_state": scanState, "download_url": "/api/v1/files/" + fileID.String()})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	fileID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var owner uuid.UUID
	var orderID *uuid.UUID
	var name, mimeType, scanState, driver, visibility string
	var data []byte
	err := s.DB.QueryRow(r.Context(), `SELECT owner_id,order_id,original_name,mime_type,scan_state,storage_driver,data,COALESCE(metadata->>'visibility','private') FROM file_objects WHERE id=$1`, fileID).Scan(&owner, &orderID, &name, &mimeType, &scanState, &driver, &data, &visibility)
	if err != nil {
		writeError(w, 404, "file_not_found", "파일을 찾을 수 없습니다.")
		return
	}
	allowed := visibility == "public" || p.UserID == owner || hasPermission(p, "orders.manage")
	if !allowed && orderID != nil {
		_, _, _, allowed = s.orderAccess(r, *orderID, p)
	}
	if !allowed {
		writeError(w, 403, "file_access_denied", "이 파일을 받을 수 없습니다.")
		return
	}
	if scanState != "clean" {
		writeError(w, 423, "file_not_cleared", "악성코드 검사가 완료되지 않은 파일입니다.")
		return
	}
	if driver != "database" {
		writeError(w, 501, "storage_adapter_required", "파일 Storage Adapter가 필요합니다.")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	disposition := "attachment"
	if visibility == "public" && strings.HasPrefix(mimeType, "image/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	cache := "private, no-store"
	if visibility == "public" {
		cache = "public, max-age=3600"
	}
	w.Header().Set("Cache-Control", cache)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

// withinUploadQuota checks the two ceilings a per file size limit cannot: how
// many files one order may hold, and how much one account may store per day.
func (s *Server) withinUploadQuota(r *http.Request, policy map[string]any, orderID *uuid.UUID, size int64) (string, bool) {
	p, _ := principalFrom(r.Context())
	if maxFiles := intSetting(policy, "max_files_per_order", 100); orderID != nil && maxFiles > 0 {
		var existing int
		if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM file_objects WHERE order_id=$1`, *orderID).Scan(&existing); err == nil && existing >= maxFiles {
			return fmt.Sprintf("이 주문에는 파일을 %d개까지 첨부할 수 있습니다.", maxFiles), false
		}
	}
	dailyMB := intSetting(policy, "daily_upload_mb_per_user", 500)
	if dailyMB <= 0 {
		return "", true
	}
	limit := int64(dailyMB) * 1024 * 1024
	var used int64
	if err := s.DB.QueryRow(r.Context(), `SELECT COALESCE(sum(size_bytes),0) FROM file_objects WHERE owner_id=$1 AND created_at >= now()-interval '24 hours'`, p.UserID).Scan(&used); err != nil {
		return "", true
	}
	if used+size > limit {
		return fmt.Sprintf("하루에 올릴 수 있는 용량 %dMB를 초과했습니다.", dailyMB), false
	}
	return "", true
}
