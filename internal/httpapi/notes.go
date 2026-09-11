package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxNoteBody = 4000

var noteSubjects = map[string]bool{"user": true, "order": true, "talent": true, "dispute": true, "report": true}

// listOperatorNotes is the running record on one subject. Notes are visible to
// every operator on purpose: a note only one person can see is a note the next
// person on shift does not have.
func (s *Server) listOperatorNotes(w http.ResponseWriter, r *http.Request) {
	subjectType := strings.TrimSpace(r.URL.Query().Get("subject_type"))
	subjectID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("subject_id")))
	if !noteSubjects[subjectType] || err != nil {
		writeError(w, 400, "invalid_subject", "메모를 조회할 대상을 확인해 주세요.")
		return
	}
	rows, queryErr := s.DB.Query(r.Context(), `SELECT n.id,n.body,n.pinned,n.created_at,n.author_id,u.display_name
		FROM operator_notes n JOIN users u ON u.id=n.author_id
		WHERE n.subject_type=$1 AND n.subject_id=$2
		ORDER BY n.pinned DESC,n.created_at DESC LIMIT $3`, subjectType, subjectID, queryLimit(r, 50, 200))
	if queryErr != nil {
		writeError(w, 500, "query_failed", "메모를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, author uuid.UUID
		var body, authorName string
		var pinned bool
		var created time.Time
		if rows.Scan(&id, &body, &pinned, &created, &author, &authorName) == nil {
			items = append(items, map[string]any{"id": id, "body": body, "pinned": pinned,
				"created_at": created, "author_id": author, "author_name": authorName})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createOperatorNote(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		SubjectType string `json:"subject_type"`
		SubjectID   string `json:"subject_id"`
		Body        string `json:"body"`
		Pinned      bool   `json:"pinned"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	subjectID, err := uuid.Parse(strings.TrimSpace(in.SubjectID))
	if !noteSubjects[in.SubjectType] || err != nil {
		writeError(w, 400, "invalid_subject", "메모를 남길 대상을 확인해 주세요.")
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if len([]rune(in.Body)) < 2 || len([]rune(in.Body)) > maxNoteBody {
		writeError(w, 400, "invalid_note", "메모를 2자 이상 4,000자 이하로 입력해 주세요.")
		return
	}
	id := uuid.New()
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO operator_notes(id,subject_type,subject_id,author_id,body,pinned) VALUES($1,$2,$3,$4,$5,$6)`,
		id, in.SubjectType, subjectID, p.UserID, in.Body, in.Pinned); err != nil {
		writeError(w, 500, "note_failed", "메모를 저장하지 못했습니다.")
		return
	}
	s.audit(r, "operator_note.create", in.SubjectType, subjectID.String(), nil, map[string]any{"note_id": id, "pinned": in.Pinned}, "success")
	writeJSON(w, 201, map[string]any{"id": id})
}

// deleteOperatorNote is limited to the person who wrote it. A note is another
// operator's judgement on a case, and removing it should not be something a
// colleague can do quietly on their behalf.
func (s *Server) deleteOperatorNote(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM operator_notes WHERE id=$1 AND author_id=$2`, id, p.UserID)
	if err != nil {
		writeError(w, 500, "note_failed", "메모를 삭제하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 403, "note_not_yours", "본인이 남긴 메모만 삭제할 수 있습니다.")
		return
	}
	s.audit(r, "operator_note.delete", "operator_note", id.String(), nil, nil, "success")
	w.WriteHeader(http.StatusNoContent)
}
