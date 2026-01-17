import { useState, useCallback, useRef } from 'react';
import { FileUpload, UploadState, ChunkInfo, ToolDefinition, ValidationResult } from '@/types';

/**
 * Custom hook for managing file uploads with chunked upload support.
 * This hook manages UI state for uploads. Actual upload logic would be
 * implemented in a service layer that interacts with the backend.
 */

const CHUNK_SIZE = 1024 * 1024; // 1MB chunks

const generateId = () => `file-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;

const createChunks = (file: File): ChunkInfo[] => {
  const chunks: ChunkInfo[] = [];
  const totalChunks = Math.ceil(file.size / CHUNK_SIZE);

  for (let i = 0; i < totalChunks; i++) {
    chunks.push({
      index: i,
      start: i * CHUNK_SIZE,
      end: Math.min((i + 1) * CHUNK_SIZE, file.size),
      uploaded: false,
    });
  }

  return chunks;
};

interface UseFileUploadOptions {
  tool: ToolDefinition;
  onValidationError?: (errors: string[]) => void;
}

interface UseFileUploadReturn {
  files: FileUpload[];
  addFiles: (newFiles: File[]) => ValidationResult;
  removeFile: (fileId: string) => void;
  clearFiles: () => void;
  reorderFiles: (fromIndex: number, toIndex: number) => void;
  startUpload: (fileId: string) => void;
  pauseUpload: (fileId: string) => void;
  resumeUpload: (fileId: string) => void;
  retryUpload: (fileId: string) => void;
  cancelUpload: (fileId: string) => void;
  updateProgress: (fileId: string, loaded: number, total: number) => void;
  setUploadState: (fileId: string, state: UploadState, error?: string) => void;
  setServerFileId: (fileId: string, serverFileId: string) => void;
  isUploading: boolean;
  uploadedCount: number;
  totalCount: number;
  canProceed: boolean;
}

export const useFileUpload = ({ tool, onValidationError }: UseFileUploadOptions): UseFileUploadReturn => {
  const [files, setFiles] = useState<FileUpload[]>([]);
  const uploadControllers = useRef<Map<string, AbortController>>(new Map());

  const validateFiles = useCallback(
    (newFiles: File[]): ValidationResult => {
      const errors: { field: string; message: string; code: string }[] = [];

      // Check total file count
      const totalFiles = files.length + newFiles.length;
      if (totalFiles > tool.maxFiles) {
        errors.push({
          field: 'files',
          message: `Maximum ${tool.maxFiles} files allowed. You have ${files.length} files and tried to add ${newFiles.length} more.`,
          code: 'MAX_FILES_EXCEEDED',
        });
      }

      // Validate each file
      newFiles.forEach((file, index) => {
        // Check file type
        const isValidType = tool.acceptedFileTypes.some((type) => {
          if (type.startsWith('.')) {
            return file.name.toLowerCase().endsWith(type.toLowerCase());
          }
          return file.type === type;
        });

        if (!isValidType) {
          errors.push({
            field: `file-${index}`,
            message: `"${file.name}" is not a valid file type. Accepted types: ${tool.acceptedFileTypes.join(', ')}`,
            code: 'INVALID_FILE_TYPE',
          });
        }

        // Check file size
        if (file.size > tool.maxFileSize) {
          const maxSizeMB = Math.round(tool.maxFileSize / (1024 * 1024));
          const fileSizeMB = (file.size / (1024 * 1024)).toFixed(1);
          errors.push({
            field: `file-${index}`,
            message: `"${file.name}" (${fileSizeMB}MB) exceeds the ${maxSizeMB}MB limit.`,
            code: 'FILE_TOO_LARGE',
          });
        }

        // Check for duplicates
        const isDuplicate = files.some(
          (existing) => existing.file.name === file.name && existing.file.size === file.size
        );
        if (isDuplicate) {
          errors.push({
            field: `file-${index}`,
            message: `"${file.name}" has already been added.`,
            code: 'DUPLICATE_FILE',
          });
        }
      });

      return {
        isValid: errors.length === 0,
        errors,
      };
    },
    [files, tool]
  );

  const addFiles = useCallback(
    (newFiles: File[]): ValidationResult => {
      const validation = validateFiles(newFiles);

      if (validation.isValid) {
        const fileUploads: FileUpload[] = newFiles.map((file) => ({
          id: generateId(),
          file,
          state: 'idle' as UploadState,
          progress: { loaded: 0, total: file.size, percentage: 0 },
          chunks: createChunks(file),
          currentChunkIndex: 0,
        }));

        setFiles((prev) => [...prev, ...fileUploads]);
      } else if (onValidationError) {
        onValidationError(validation.errors.map((e) => e.message));
      }

      return validation;
    },
    [validateFiles, onValidationError]
  );

  const removeFile = useCallback((fileId: string) => {
    // Cancel any ongoing upload
    const controller = uploadControllers.current.get(fileId);
    if (controller) {
      controller.abort();
      uploadControllers.current.delete(fileId);
    }

    setFiles((prev) => prev.filter((f) => f.id !== fileId));
  }, []);

  const clearFiles = useCallback(() => {
    // Cancel all ongoing uploads
    uploadControllers.current.forEach((controller) => controller.abort());
    uploadControllers.current.clear();
    setFiles([]);
  }, []);

  const reorderFiles = useCallback((fromIndex: number, toIndex: number) => {
    setFiles((prev) => {
      const newFiles = [...prev];
      const [removed] = newFiles.splice(fromIndex, 1);
      newFiles.splice(toIndex, 0, removed);
      return newFiles;
    });
  }, []);

  const startUpload = useCallback((fileId: string) => {
    const controller = new AbortController();
    uploadControllers.current.set(fileId, controller);

    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId
          ? { ...f, state: 'uploading' as UploadState, error: undefined }
          : f
      )
    );
  }, []);

  const pauseUpload = useCallback((fileId: string) => {
    const controller = uploadControllers.current.get(fileId);
    if (controller) {
      controller.abort();
    }

    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId ? { ...f, state: 'paused' as UploadState } : f
      )
    );
  }, []);

  const resumeUpload = useCallback((fileId: string) => {
    startUpload(fileId);
  }, [startUpload]);

  const retryUpload = useCallback((fileId: string) => {
    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId
          ? {
              ...f,
              state: 'idle' as UploadState,
              error: undefined,
              progress: { loaded: 0, total: f.file.size, percentage: 0 },
              currentChunkIndex: 0,
              chunks: f.chunks.map((c) => ({ ...c, uploaded: false })),
            }
          : f
      )
    );
    startUpload(fileId);
  }, [startUpload]);

  const cancelUpload = useCallback((fileId: string) => {
    removeFile(fileId);
  }, [removeFile]);

  const updateProgress = useCallback((fileId: string, loaded: number, total: number) => {
    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId
          ? {
              ...f,
              progress: {
                loaded,
                total,
                percentage: Math.round((loaded / total) * 100),
              },
            }
          : f
      )
    );
  }, []);

  const setUploadState = useCallback((fileId: string, state: UploadState, error?: string) => {
    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId
          ? {
              ...f,
              state,
              error,
              uploadedAt: state === 'completed' ? new Date() : f.uploadedAt,
            }
          : f
      )
    );
  }, []);

  const setServerFileId = useCallback((fileId: string, serverFileId: string) => {
    setFiles((prev) =>
      prev.map((f) =>
        f.id === fileId ? { ...f, serverFileId } : f
      )
    );
  }, []);

  const isUploading = files.some((f) => f.state === 'uploading');
  const uploadedCount = files.filter((f) => f.state === 'completed').length;
  const totalCount = files.length;
  const canProceed =
    files.length >= tool.minFiles &&
    files.length <= tool.maxFiles &&
    files.every((f) => f.state === 'completed' || f.state === 'idle');

  return {
    files,
    addFiles,
    removeFile,
    clearFiles,
    reorderFiles,
    startUpload,
    pauseUpload,
    resumeUpload,
    retryUpload,
    cancelUpload,
    updateProgress,
    setUploadState,
    setServerFileId,
    isUploading,
    uploadedCount,
    totalCount,
    canProceed,
  };
};
