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

// uploadChunkLua atomically validates the upload exists, records the chunk index,
// refreshes TTLs, and returns the upload state — all in one round-trip.
var uploadChunkLua = redis.NewScript(`
local stateKey = KEYS[1]
local chunkKey = KEYS[2]
local chunkIdx = ARGV[1]
local ttl = tonumber(ARGV[2])

-- Check upload exists
local exists = redis.call('EXISTS', stateKey)
if exists == 0 then
    return redis.error_reply("NOT_FOUND")
end

-- Record chunk and refresh TTLs
redis.call('SADD', chunkKey, chunkIdx)
redis.call('EXPIRE', chunkKey, ttl)
redis.call('EXPIRE', stateKey, ttl)

-- Return state fields
local state = redis.call('HGETALL', stateKey)
local received = redis.call('SCARD', chunkKey)
state[#state+1] = 'receivedChunks'
state[#state+1] = tostring(received)
return state
`)

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
		response.BadRequest(c, "INVALID_INPUT", "Invalid upload request. Please try again.")
		return
	}
	if req.FileName == "" || req.FileSize <= 0 || req.TotalChunks <= 0 {
		response.BadRequest(c, "INVALID_INPUT", "Missing file information. Please try again.")
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
		response.InternalError(c, "SERVER_ERROR", "Could not start the upload. Please try again.")
		return
	}

	response.Created(c, "Upload started successfully", gin.H{"uploadId": uploadID})
}

func UploadChunk(c *gin.Context) {
	uploadID := c.Param("uploadId")
	indexStr := c.Query("index")
	if uploadID == "" || indexStr == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload information is missing. Please restart the upload.")
		return
	}
	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		response.BadRequest(c, "INVALID_INPUT", "Upload error. Please restart the upload.")
		return
	}

	file, err := c.FormFile("chunk")
	if err != nil {
		response.BadRequest(c, "INVALID_INPUT", "Upload data is missing. Please try again.")
		return
	}

	tmpDir := uploadTmpDir()
	chunkDir := filepath.Join(tmpDir, uploadID)
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll(chunkDir, 0750); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Something went wrong during upload. Please try again.")
		return
	}
	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%06d.part", index))
	if err := c.SaveUploadedFile(file, chunkPath); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Upload interrupted. Please try again.")
		return
	}

	ctx := c.Request.Context()
	ttlSeconds := int(uploadTTL().Seconds())
	result, err := uploadChunkLua.Run(ctx, redisstore.Client,
		[]string{uploadStateKey(uploadID), uploadChunkSetKey(uploadID)},
		index, ttlSeconds,
	).StringSlice()
	if err != nil {
		if err.Error() == "NOT_FOUND" {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
			return
		}
		response.InternalError(c, "SERVER_ERROR", "Something went wrong during upload. Please try again.")
		return
	}

	// Parse Lua result (HGETALL-style key-value pairs + receivedChunks)
	state := make(map[string]string)
	for i := 0; i+1 < len(result); i += 2 {
		state[result[i]] = result[i+1]
	}

	totalChunks, _ := strconv.Atoi(state["totalChunks"])
	fileSize, _ := strconv.ParseInt(state["fileSize"], 10, 64)
	receivedChunks, _ := strconv.ParseInt(state["receivedChunks"], 10, 64)
	status := UploadStatus{
		UploadID:       uploadID,
		FileName:       state["fileName"],
		FileSize:       fileSize,
		TotalChunks:    totalChunks,
		ReceivedChunks: receivedChunks,
		Complete:        int(receivedChunks) == totalChunks && totalChunks > 0,
	}
	response.OK(c, "Upload in progress", status)
}

func GetUploadStatus(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
		} else {
			response.InternalError(c, "SERVER_ERROR", "Upload session expired. Please upload your file again.")
		}
		return
	}

	status := uploadStatusFromState(uploadID, state, ctx)
	response.OK(c, "Upload status loaded", status)
}

func CompleteUpload(c *gin.Context) {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		response.BadRequest(c, "INVALID_INPUT", "Upload session not found. Please try again.")
		return
	}

	ctx := c.Request.Context()
	state, err := fetchUploadState(ctx, uploadID)
	if err != nil {
		if err == redis.Nil {
			response.NotFound(c, "NOT_FOUND", "Upload session expired. Please upload your file again.")
		} else {
			response.InternalError(c, "SERVER_ERROR", "Upload session expired. Please upload your file again.")
		}
		return
	}

	status := uploadStatusFromState(uploadID, state, ctx)
	if !status.Complete {
		response.BadRequest(c, "BAD_REQUEST", "Upload is not complete yet. Please wait for all parts to finish.")
		return
	}

	uploadDir := uploadBaseDir()
	jobDir := filepath.Join(uploadDir, uploadID)
	// Fix #17: Use 0750 instead of os.ModePerm
	if err := os.MkdirAll(jobDir, 0750); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Something went wrong. Please try again.")
		return
	}

	finalPath := filepath.Join(jobDir, status.FileName)
	if err := assembleChunks(uploadID, status.TotalChunks, finalPath); err != nil {
		response.InternalError(c, "SERVER_ERROR", "Could not process your upload. Please try again.")
		return
	}

	if info, err := os.Stat(finalPath); err == nil {
		maxBytes := maxUploadBytes()
		if info.Size() > maxBytes {
			_ = os.Remove(finalPath)
			response.BadRequest(c, "FILE_TOO_LARGE", "File is too large. Please upload a smaller file.")
			return
		}
	}

	_ = cleanupChunks(uploadID)
	response.OK(c, "File uploaded successfully!", gin.H{"uploadId": uploadID})
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
