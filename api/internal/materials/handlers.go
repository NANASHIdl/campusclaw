// Package materials 提供材料列表、详情、下载与教师上传。
//
// 班级隔离在服务端强制实施：列表只用会话班级过滤；
// 详情与下载先取行再校验归属，跨班与不存在对外返回同构 404。
package materials

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/index"
)

// Handler 持有材料接口所需的依赖。
type Handler struct {
	DB             *sql.DB
	UploadDir      string
	MaxUploadBytes int64
	// Indexer 在材料入库后建立切片与向量索引；为 nil 时跳过索引（仅测试场景）。
	Indexer *index.Service
}

func NewHandler(db *sql.DB, uploadDir string, maxUploadBytes int64, indexer *index.Service) *Handler {
	return &Handler{DB: db, UploadDir: uploadDir, MaxUploadBytes: maxUploadBytes, Indexer: indexer}
}

type listItem struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Uploader  string `json:"uploader"`
	CreatedAt string `json:"created_at"`
}

type detailItem struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	ClassID   int64  `json:"class_id"`
	CreatedAt string `json:"created_at"`
}

// List 处理 GET /api/materials：只返回会话班级的记录。
//
// @Summary      列出本班材料
// @Description  只返回会话所属班级的材料，按创建时间倒序。
// @Tags         materials
// @Produce      json
// @Security     bearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/materials [get]
// @ID           MAT-001
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}

	rows, err := h.DB.Query(
		`SELECT m.id, m.title, COALESCE(u.username, ''), m.created_at
		   FROM materials m
		   LEFT JOIN users u ON u.id = m.uploader_id
		  WHERE m.class_id = ?
		  ORDER BY m.created_at DESC, m.id DESC`,
		user.ClassID,
	)
	if err != nil {
		log.Printf("list materials: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer rows.Close()

	items := make([]listItem, 0)
	for rows.Next() {
		var it listItem
		var createdAt any
		if err := rows.Scan(&it.ID, &it.Title, &it.Uploader, &createdAt); err != nil {
			log.Printf("scan material row: %v", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
			return
		}
		it.CreatedAt = formatTime(createdAt)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate material rows: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// 空结果集同样返回 200。
	httpx.JSON(w, http.StatusOK, map[string]any{"materials": items})
}

// Detail 处理 GET /api/materials/{id}：先取行再校验班级归属。
//
// @Summary      读取本班材料详情
// @Description  跨班与不存在返回同一个 404。
// @Tags         materials
// @Produce      json
// @Security     bearerAuth
// @Param        id path int true "材料编号"
// @Success      200 {object} materials.detailItem
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/materials/{id} [get]
// @ID           MAT-002
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}

	row, found, err := h.fetchMaterial(id)
	if err != nil {
		log.Printf("fetch material %d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !found {
		log.Printf("material access: reason=not_found material_id=%d user=%s", id, user.Username)
		httpx.NotFound(w)
		return
	}
	if row.ClassID != user.ClassID {
		// 跨班：对外与「不存在」完全同构，真实原因仅写日志
		log.Printf("material access: reason=cross_class material_id=%d owner_class=%d user_class=%d user=%s",
			id, row.ClassID, user.ClassID, user.Username)
		httpx.NotFound(w)
		return
	}

	httpx.JSON(w, http.StatusOK, detailItem{
		ID: row.ID, Title: row.Title, ClassID: row.ClassID, CreatedAt: formatTime(row.CreatedAt),
	})
}

// Download 处理 GET /api/materials/{id}/file。
// 必须在读取文件字节之前完成认证（中间件已做）与班级归属校验。
//
// @Summary      下载本班材料原文
// @Description  读取文件前完成认证与班级校验；跨班与不存在返回同一个 404。上传目录不作为静态资源暴露。
// @Tags         materials
// @Produce      application/octet-stream
// @Security     bearerAuth
// @Param        id path int true "材料编号"
// @Success      200 {file} file "材料原文"
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/materials/{id}/file [get]
// @ID           MAT-003
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}

	row, found, err := h.fetchMaterial(id)
	if err != nil {
		log.Printf("fetch material %d: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !found || row.ClassID != user.ClassID {
		reason := "not_found"
		if found {
			reason = "cross_class"
		}
		log.Printf("material download: reason=%s material_id=%d user=%s", reason, id, user.Username)
		httpx.NotFound(w)
		return
	}

	f, err := os.Open(row.FilePath)
	if err != nil {
		log.Printf("open material file %q: %v", row.FilePath, err)
		httpx.NotFound(w)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		httpx.NotFound(w)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(row.Title))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("stream material file %d: %v", id, err)
	}
}

// Upload 处理 POST /api/materials/upload。
//
// @Summary      上传本班材料
// @Description  仅教师，学生调用返回 403。仅接受 .txt / .md 且不超过大小上限；分段参数在落盘与写库之前校验，越界返回 400。入库后自动切分、嵌入并写入向量库。
// @Tags         materials
// @Accept       multipart/form-data
// @Produce      json
// @Security     bearerAuth
// @Param        file              formData file   true  "材料文件（.txt / .md）"
// @Param        chunk_strategy    formData string false "分段策略：auto / custom / hierarchy"
// @Param        max_chars         formData int    false "分段最大长度（custom/hierarchy，100–2000）"
// @Param        overlap_percent   formData int    false "分段重叠度百分比（0–50）"
// @Param        separator         formData string false "分段标识符（custom）"
// @Param        remove_urls       formData bool   false "删除 URL 与电子邮箱（custom）"
// @Param        collapse_whitespace formData bool false "折叠连续空白（custom）"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      403 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/materials/upload [post]
// @ID           MAT-004
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	// 角色校验先于任何磁盘/数据库写入：学生请求在此被拒绝。
	if !user.IsTeacher() {
		log.Printf("upload rejected: reason=role_forbidden user=%s role=%s", user.Username, user.Role)
		httpx.Error(w, http.StatusForbidden, httpx.CodeForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}
	defer file.Close()

	// 白名单校验在落盘之前完成。
	if !AllowedExtension(header.Filename) {
		log.Printf("upload rejected: reason=unsupported_type name=%q user=%s", header.Filename, user.Username)
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}

	// 分段参数同样在落盘之前校验：越界直接拒绝，不产生文件与记录。
	chunkOpt, err := chunkOptionsFromForm(r)
	if err != nil {
		log.Printf("upload rejected: reason=bad_chunk_params %v user=%s", err, user.Username)
		httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	raw, err := io.ReadAll(io.LimitReader(file, h.MaxUploadBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}
	if int64(len(raw)) > h.MaxUploadBytes {
		log.Printf("upload rejected: reason=too_large user=%s", user.Username)
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}
	if _, err := Parse(header.Filename, raw); err != nil {
		log.Printf("upload rejected: reason=%v user=%s", err, user.Username)
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest)
		return
	}

	title := TitleFromFilename(header.Filename)
	// 班级只取自会话，表单中若带 class 字段一律忽略。
	classID := user.ClassID

	destDir := filepath.Join(h.UploadDir, strconv.FormatInt(classID, 10))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Printf("create upload dir: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	destPath := filepath.Join(destDir, randomPrefix()+"_"+SafeStoredName(header.Filename))
	if err := os.WriteFile(destPath, raw, 0o644); err != nil {
		log.Printf("write upload file: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}

	materialID, entryID, err := h.insertMaterialWithKnowledge(title, classID, destPath, user.ID, string(raw))
	if err != nil {
		// 事务失败：清理已写入的文件，避免「有文件无记录」的孤儿状态
		if rmErr := os.Remove(destPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			log.Printf("cleanup orphan file %q: %v", destPath, rmErr)
		}
		log.Printf("insert material: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}

	// 原文与正文已提交；此后建立切片与向量索引。
	// 索引失败不影响材料可用性（课件：材料记录仍然保留，切片标记 failed）。
	chunkCount := 0
	if h.Indexer != nil {
		n, idxErr := h.Indexer.IndexMaterial(r.Context(), materialID, classID, entryID, string(raw), chunkOpt)
		if idxErr != nil {
			log.Printf("索引失败（材料已保留）material_id=%d: %v", materialID, idxErr)
		} else {
			chunkCount = n
		}
	}

	log.Printf("upload ok: material_id=%d class_id=%d chunks=%d strategy=%s user=%s title=%q",
		materialID, classID, chunkCount, chunkOpt.Strategy, user.Username, title)
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"material_id": materialID,
		"title":       title,
		"chunk_count": chunkCount,
	})
}

// --- 内部辅助 ---------------------------------------------------------------

type materialRow struct {
	ID        int64
	Title     string
	ClassID   int64
	FilePath  string
	CreatedAt any
}

func (h *Handler) fetchMaterial(id int64) (materialRow, bool, error) {
	var m materialRow
	err := h.DB.QueryRow(
		`SELECT id, title, class_id, file_path, created_at FROM materials WHERE id = ?`, id,
	).Scan(&m.ID, &m.Title, &m.ClassID, &m.FilePath, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return materialRow{}, false, nil
	}
	if err != nil {
		return materialRow{}, false, err
	}
	return m, true, nil
}

// insertMaterialWithKnowledge 在单个事务中写入两张带租户列的表。
func (h *Handler) insertMaterialWithKnowledge(title string, classID int64, filePath string, uploaderID int64, body string) (int64, int64, error) {
	tx, err := h.DB.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // Commit 成功后 Rollback 是空操作
	}()

	res, err := tx.Exec(
		`INSERT INTO materials (title, class_id, file_path, uploader_id) VALUES (?, ?, ?, ?)`,
		title, classID, filePath, uploaderID,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("写入 materials 失败: %w", err)
	}
	materialID, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	entry, err := tx.Exec(
		`INSERT INTO knowledge_entries (material_id, class_id, body_text) VALUES (?, ?, ?)`,
		materialID, classID, body,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("写入 knowledge_entries 失败: %w", err)
	}
	entryID, err := entry.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("提交事务失败: %w", err)
	}
	return materialID, entryID, nil
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func randomPrefix() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "upload"
	}
	return hex.EncodeToString(buf)
}

func formatTime(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		if s, ok := v.(interface{ Format(string) string }); ok {
			return s.Format("2006-01-02 15:04:05")
		}
		return fmt.Sprint(t)
	}
}

// contentDisposition 生成兼容中文文件名的下载头。
func contentDisposition(title string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 128 && r != '"' && r != '\\' {
			return r
		}
		return -1
	}, title)
	if ascii == "" {
		ascii = "material"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, urlEscape(title))
}

func urlEscape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0F])
	}
	return b.String()
}
