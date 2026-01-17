import { useState } from 'react';
import { TOOLS } from '@/config/tools';
import { useFileUpload } from '@/hooks/useFileUpload';
import { useJob } from '@/hooks/useJob';
import { ToolPageLayout } from './ToolPageLayout';
import { FileDropzone } from '@/components/common/FileDropzone';
import { FileList } from '@/components/common/FileList';
import { JobProgress } from '@/components/common/JobProgress';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';
import { Layers, Plus, Trash2 } from 'lucide-react';

const tool = TOOLS.merge;

export const MergeTool = () => {
  const {
    files,
    addFiles,
    removeFile,
    clearFiles,
    reorderFiles,
    isUploading,
    canProceed,
  } = useFileUpload({
    tool,
    onValidationError: (errors) => {
      errors.forEach((error) => toast.error(error));
    },
  });

  const { job, createJob, cancelJob, retryJob, resetJob } = useJob({
    onComplete: () => {
      toast.success('PDF files merged successfully!');
    },
    onError: (error) => {
      toast.error(error);
    },
  });

  const handleFilesSelected = (selectedFiles: File[]) => {
    addFiles(selectedFiles);
  };

  const handleMerge = () => {
    if (files.length < 2) {
      toast.error('Please add at least 2 PDF files to merge');
      return;
    }

    // Create job with file IDs and merge order
    createJob(
      tool.id,
      files.map((f) => f.id),
      { order: files.map((f) => f.id) }
    );
  };

  const handleStartOver = () => {
    resetJob();
    clearFiles();
  };

  const hasFiles = files.length > 0;
  const isProcessing = job && !['completed', 'failed'].includes(job.state);
  const isComplete = job?.state === 'completed';

  return (
    <ToolPageLayout tool={tool}>
      <div className="max-w-3xl mx-auto">
        {/* Upload section */}
        {!job && (
          <>
            <FileDropzone
              tool={tool}
              onFilesSelected={handleFilesSelected}
              disabled={isUploading}
              compact={hasFiles}
              className="mb-6"
            />

            {hasFiles && (
              <>
                {/* File list header */}
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <Layers className="w-5 h-5 text-tool-merge" />
                    <span className="font-medium">
                      {files.length} file{files.length !== 1 ? 's' : ''} selected
                    </span>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={clearFiles}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <Trash2 className="w-4 h-4 mr-2" />
                    Clear all
                  </Button>
                </div>

                {/* Reorderable file list */}
                <FileList
                  files={files}
                  onRemove={removeFile}
                  onReorder={reorderFiles}
                  showReorder
                  className="mb-6"
                />

                {/* Add more files hint */}
                <p className="text-sm text-muted-foreground mb-6 text-center">
                  Drag files to reorder. The final PDF will follow this order.
                </p>

                {/* Action buttons */}
                <div className="flex gap-3">
                  <Button
                    variant="outline"
                    className="flex-1"
                    onClick={() =>
                      document.querySelector<HTMLInputElement>('input[type="file"]')?.click()
                    }
                  >
                    <Plus className="w-4 h-4 mr-2" />
                    Add more files
                  </Button>
                  <Button
                    className="flex-1 bg-tool-merge hover:bg-tool-merge/90"
                    onClick={handleMerge}
                    disabled={!canProceed || files.length < 2}
                  >
                    <Layers className="w-4 h-4 mr-2" />
                    Merge {files.length} PDFs
                  </Button>
                </div>
              </>
            )}
          </>
        )}

        {/* Job progress */}
        {job && (
          <div className="space-y-6">
            <JobProgress
              job={job}
              onCancel={cancelJob}
              onRetry={retryJob}
              onDownload={() => {
                // In production, this would trigger actual download
                toast.success('Download started');
              }}
            />

            {(isComplete || job.state === 'failed') && (
              <Button
                variant="outline"
                className="w-full"
                onClick={handleStartOver}
              >
                Start over with new files
              </Button>
            )}
          </div>
        )}
      </div>
    </ToolPageLayout>
  );
};
