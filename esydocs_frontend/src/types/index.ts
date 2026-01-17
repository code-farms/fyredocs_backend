/**
 * Core type definitions for the PDF Tools platform.
 * These types establish strict boundaries between UI state, upload state, and job state.
 */

// ============================================================================
// TOOL DEFINITIONS
// ============================================================================

export type ToolId =
  | 'merge'
  | 'split'
  | 'compress'
  | 'pdf-to-word'
  | 'word-to-pdf'
  | 'pdf-to-excel'
  | 'excel-to-pdf'
  | 'pdf-to-image'
  | 'image-to-pdf'
  | 'reorder'
  | 'rotate'
  | 'ocr'
  | 'watermark'
  | 'password-protect';

export type ToolCategory = 'merge' | 'split' | 'compress' | 'convert' | 'organize' | 'security' | 'ocr' | 'watermark';

export interface ToolDefinition {
  id: ToolId;
  name: string;
  description: string;
  category: ToolCategory;
  icon: string;
  acceptedFileTypes: string[];
  maxFiles: number;
  minFiles: number;
  maxFileSize: number; // in bytes
  route: string;
}

// ============================================================================
// FILE & UPLOAD STATE MACHINE
// ============================================================================

/**
 * Upload state follows a strict state machine:
 * idle → uploading → (paused | completed | failed)
 * paused → uploading
 * failed → uploading (retry)
 */
export type UploadState = 'idle' | 'uploading' | 'paused' | 'completed' | 'failed';

export interface UploadProgress {
  loaded: number;
  total: number;
  percentage: number;
}

export interface ChunkInfo {
  index: number;
  start: number;
  end: number;
  uploaded: boolean;
}

export interface FileUpload {
  id: string;
  file: File;
  state: UploadState;
  progress: UploadProgress;
  chunks: ChunkInfo[];
  currentChunkIndex: number;
  error?: string;
  uploadedAt?: Date;
  serverFileId?: string; // Returned by backend after successful upload
}

// ============================================================================
// JOB STATE MACHINE
// ============================================================================

/**
 * Job state follows a strict state machine:
 * pending → queued → processing → (completed | failed)
 * 
 * pending: Job created but not yet submitted
 * queued: Job submitted, waiting in queue
 * processing: Backend is actively processing
 * completed: Job finished successfully
 * failed: Job failed (terminal or retryable)
 */
export type JobState = 'pending' | 'queued' | 'processing' | 'completed' | 'failed';

export interface JobProgress {
  currentStep: string;
  totalSteps: number;
  completedSteps: number;
  percentage: number;
  estimatedTimeRemaining?: number; // in seconds
}

export interface JobResult {
  downloadUrl: string;
  fileName: string;
  fileSize: number;
  expiresAt: Date;
}

export interface Job {
  id: string;
  toolId: ToolId;
  state: JobState;
  progress: JobProgress;
  createdAt: Date;
  updatedAt: Date;
  completedAt?: Date;
  result?: JobResult;
  error?: JobError;
  fileIds: string[];
  options: ToolOptions;
}

export interface JobError {
  code: string;
  message: string;
  isRetryable: boolean;
  details?: Record<string, unknown>;
}

// ============================================================================
// TOOL-SPECIFIC OPTIONS
// ============================================================================

export interface MergeOptions {
  order: string[]; // File IDs in desired order
}

export interface SplitOptions {
  mode: 'all' | 'range' | 'extract';
  ranges?: string; // e.g., "1-3, 5, 7-9"
  extractPages?: number[];
}

export interface CompressOptions {
  quality: 'low' | 'medium' | 'high' | 'extreme';
}

export interface WatermarkOptions {
  type: 'text' | 'image';
  text?: string;
  imageFileId?: string;
  position: 'center' | 'diagonal' | 'tiled';
  opacity: number;
  fontSize?: number;
  color?: string;
}

export interface PasswordProtectOptions {
  password: string;
  confirmPassword: string;
  permissions?: {
    allowPrint: boolean;
    allowCopy: boolean;
    allowEdit: boolean;
  };
}

export interface OcrOptions {
  language: string;
  enhanceScans: boolean;
}

export interface RotateOptions {
  rotation: 90 | 180 | 270;
  applyToPages: 'all' | number[];
}

export interface ReorderOptions {
  pageOrder: number[];
}

export interface ConvertOptions {
  format: 'docx' | 'xlsx' | 'png' | 'jpg' | 'pdf';
  quality?: 'low' | 'medium' | 'high';
}

export type ToolOptions =
  | MergeOptions
  | SplitOptions
  | CompressOptions
  | WatermarkOptions
  | PasswordProtectOptions
  | OcrOptions
  | RotateOptions
  | ReorderOptions
  | ConvertOptions
  | Record<string, never>; // Empty options for simple tools

// ============================================================================
// UI STATE
// ============================================================================

export interface DropzoneState {
  isDragActive: boolean;
  isDragReject: boolean;
}

export interface FileListState {
  selectedFileIds: string[];
  sortOrder: 'custom' | 'name' | 'size' | 'date';
}

export interface ToolPageState {
  currentStep: 'upload' | 'configure' | 'processing' | 'complete';
  files: FileUpload[];
  job: Job | null;
  dropzone: DropzoneState;
  fileList: FileListState;
}

// ============================================================================
// VALIDATION
// ============================================================================

export interface ValidationResult {
  isValid: boolean;
  errors: ValidationError[];
}

export interface ValidationError {
  field: string;
  message: string;
  code: string;
}

// ============================================================================
// API ABSTRACTIONS
// ============================================================================

/**
 * These interfaces define the SHAPE of backend interactions
 * without specifying concrete endpoints or schemas.
 * The actual implementation will be injected via services.
 */

export interface UploadService {
  initializeUpload(file: File, toolId: ToolId): Promise<{ uploadId: string; chunkSize: number }>;
  uploadChunk(uploadId: string, chunk: Blob, chunkIndex: number): Promise<void>;
  completeUpload(uploadId: string): Promise<{ fileId: string }>;
  cancelUpload(uploadId: string): Promise<void>;
  getUploadStatus(uploadId: string): Promise<{ uploadedChunks: number[] }>;
}

export interface JobService {
  createJob(toolId: ToolId, fileIds: string[], options: ToolOptions): Promise<{ jobId: string }>;
  getJobStatus(jobId: string): Promise<Job>;
  cancelJob(jobId: string): Promise<void>;
  retryJob(jobId: string): Promise<{ jobId: string }>;
}
