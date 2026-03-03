package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"esydocs/shared/redisstore"
	"esydocs/shared/response"
)

type UploadInitRequest struct {
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	TotalChunks int    `json:"totalChunks"`
}

type UploadStatus struct {
	UploadID       string `json:"uploadId"`
	FileName       string `json:"fileName"`
	FileSize       int64  `json:"fileSize"`
	TotalChunks    int    `json:"totalChunks"`
	ReceivedChunks int64  `json:"receivedChunks"`
	Complete       bool   `json:"complete"`
}

func InitUpload(c *gin.Context) {
	var req UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Invalid request body")
		return
	}
	if req.FileName == "" || req.FileSize <= 0 || req.TotalChunks <= 0 {
		response.BadRequest(c, "INVALID_INPUT", "fileName, fileSize, and totalChunks are required")
		return
	}

	uploadID := uuid.New().String()
	uploadKey := uploadStateKey(uploadID)

	ctx := c.Request.Context()
	pipe := redisstore.Client.TxPipeline()
	pipe.HSet(ctx, uploadKey, map[string]interface{}{
		"fileName":    filepath.Base(req.FileName),
		"fileSize":    req.FileSize,
		"totalChunks": req.TotalChunks,
		"createdAt":   time.Now().UTC().Format(time.RFC3339),
	})
	pipe.Expire(ctx, uploadKey, uploadTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Failed to initialize upload")
		return
	}

	response.Created(c, "Upload initialized", gin.H{"uploadId": uploadID})
}

func UploadChunk(c *gin.Context) {
	uploadID := c.Param("uploadId")
	indexStr := c.Query("index")
	if uploadID == "" || indexStr == "" {
		response.BadRequest(c, "INVALID_INPUT", "uploadId and chunk index are required")
		return
	}
	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		response.BadRequest(c, "INVALID_INPUT", "invalid chunk index")
		return
	}

	file, err := c.FormFile("chunk")
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "chunk is required")
		return
	}

	state, err := fetchUploadState(c.Request.Context(), uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "upload not found")
		} else {
			response.InternalError(c, "SERVER_ERROR", "upload not found")
		}
		return
	}

	tmpDir := uploadTmpDir()
	chunkDir := filepath.Join(tmpDir, uploadID)
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll(chunkDir, 0750); err != nil {
		response.InternalError(c, "SERVER_ERROR", "failed to create chunk directory")
		return
	}
	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%06d.part", index))
	if err := c.SaveUploadedFile(file, chunkPath); err != nil {
		response.InternalError(c, "SERVER_ERROR", "failed to save chunk")
		return
	}

	ctx := c.Request.Context()
	chunkSetKey := uploadChunkSetKey(uploadID)
	pipe := redisstore.Client.TxPipeline()
	pipe.SAdd(ctx, chunkSetKey, index)
	pipe.Expire(ctx, chunkSetKey, uploadTTL())
	pipe.Expire(ctx, uploadStateKey(uploadID), uploadTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		response.InternalError(c, "SERVER_ERROR", "failed to update upload state")
		return
	}

	status := uploadStatusFromState(uploadID, state, ctx)
	response.OK(c, "Chunk uploaded", status)
}

func GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "uploadId is required")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "upload not found")
		} else {
			response.InternalError(c, "SERVER_ERROR", "upload not found")
		}
		return
	}

	status := uploadStatusFromState(uploadID, state, ctx)
	response.OK(c, "Upload status retrieved", status)
}

func CompleteUpload(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "uploadId is required")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "upload not found")
		} else {
			response.InternalError(c, "SERVER_ERROR", "upload not found")
		}
		return
	}

	status := uploadStatusFromState(uploadID, state, ctx)
	if !status.Complete {
		response.BadRequest(c, "BAD_REQUEST", "upload is incomplete")
		return
	}

	uploadDir := uploadBaseDir()
	jobDir := filepath.Join(uploadDir, uploadID)
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll(jobDir, 0750); err != nil {
		response.InternalError(c, "SERVER_ERROR", "failed to create upload directory")
		return
	}

	finalPath := filepath.Join(jobDir, status.FileName)
	if err := assembleChunks(uploadID, status.TotalChunks, finalPath); err != nil {
		response.InternalError(c, "SERVER_ERROR", "failed to assemble chunks")
		return
	}

	if info, err := os.Stat(finalPath); err == nil {
		maxBytes := maxUploadBytes()
		if info.Size() > maxBytes {
			_ = os.Remove(finalPath)
			response.BadRequest(c, "FILE_TOO_LARGE", "assembled file exceeds maximum size")
			return
		}
	}

	_ = cleanupChunks(uploadID)
	response.OK(c, "Upload completed", gin.H{"uploadId": uploadID, "storedPath": finalPath})
}

func assembleChunks(uploadID string, totalChunks int, outputPath string) error {
	tmpDir := uploadTmpDir()
	chunkDir := filepath.Join(tmpDir, uploadID)
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	assembled := false
	defer func() {
		_ = out.Close()
		if !assembled {
			_ = os.Remove(outputPath)
		}
	}()

	for i := 0; i < totalChunks; i++ {
		chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%06d.part", i))
		in, err := os.Open(chunkPath)
		if err != nil {
			return err
		}
		if _, err := out.ReadFrom(in); err != nil {
			_ = in.Close()
			return err
		}
		_ = in.Close()
	}

	if err := out.Sync(); err != nil {
		return err
	}

	assembled = true
	return nil
}

func cleanupChunks(uploadID string) error {
	return os.RemoveAll(filepath.Join(uploadTmpDir(), uploadID))
}

func fetchUploadState(ctx context.Context, uploadID string) (map[string]string, error) {
	state, err := redisstore.Client.HGetAll(ctx, uploadStateKey(uploadID)).Result()
	if err != nil {
		return nil, err
	}
	if len(state) == 0 {
		return nil, redis.Nil
	}
	return state, nil
}

func uploadStatusFromState(uploadID string, state map[string]string, ctx context.Context) UploadStatus {
	totalChunks, _ := strconv.Atoi(state["totalChunks"])
	fileSize, _ := strconv.ParseInt(state["fileSize"], 10, 64)
	received := redisstore.Client.SCard(ctx, uploadChunkSetKey(uploadID)).Val()
	return UploadStatus{
		UploadID:       uploadID,
		FileName:       state["fileName"],
		FileSize:       fileSize,
		TotalChunks:    totalChunks,
		ReceivedChunks: received,
		Complete:       int(received) == totalChunks && totalChunks > 0,
	}
}

func uploadStateKey(uploadID string) string {
	return fmt.Sprintf("upload:%s", uploadID)
}

func uploadChunkSetKey(uploadID string) string {
	return fmt.Sprintf("upload:%s:chunks", uploadID)
}

func uploadTmpDir() string {
	base := uploadBaseDir()
	return filepath.Join(base, "tmp")
}

func uploadBaseDir() string {
	if value := os.Getenv("UPLOAD_DIR"); value != "" {
		return value
	}
	return "uploads"
}

func uploadTTL() time.Duration {
	ttl := os.Getenv("UPLOAD_TTL")
	if ttl == "" {
		return 2 * time.Hour
	}
	parsed, err := time.ParseDuration(ttl)
	if err != nil {
		return 2 * time.Hour
	}
	return parsed
}
