import { useState } from 'react';
import { TOOLS } from '@/config/tools';
import { useFileUpload } from '@/hooks/useFileUpload';
import { useJob } from '@/hooks/useJob';
import { ConvertOptions, ToolId } from '@/types';
import { ToolPageLayout } from './ToolPageLayout';
import { FileDropzone } from '@/components/common/FileDropzone';
import { FileList } from '@/components/common/FileList';
import { JobProgress } from '@/components/common/JobProgress';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';
import { FileText, Plus, Trash2 } from 'lucide-react';

interface ConvertToolProps {
  toolId: ToolId;
  outputFormat: 'docx' | 'xlsx' | 'png' | 'jpg' | 'pdf';
}

export const ConvertTool = ({ toolId, outputFormat }: ConvertToolProps) => {
  const tool = TOOLS[toolId];

  const {
    files,
    addFiles,
    removeFile,
    clearFiles,
    canProceed,
  } = useFileUpload({
    tool,
    onValidationError: (errors) => {
      errors.forEach((error) => toast.error(error));
    },
  });

  const { job, createJob, cancelJob, retryJob, resetJob } = useJob({
    onComplete: () => {
      toast.success('Conversion completed successfully!');
    },
  });

  const handleFilesSelected = (selectedFiles: File[]) => {
    addFiles(selectedFiles);
  };

  const handleConvert = () => {
    if (files.length === 0) {
      toast.error('Please add files to convert');
      return;
    }

    const options: ConvertOptions = {
      format: outputFormat,
      quality: 'high',
    };

    createJob(
      tool.id,
      files.map((f) => f.id),
      options
    );
  };

  const handleStartOver = () => {
    resetJob();
    clearFiles();
  };

  const hasFiles = files.length > 0;
  const isComplete = job?.state === 'completed';

  return (
    <ToolPageLayout tool={tool}>
      <div className="max-w-3xl mx-auto">
        {!job && (
          <>
            <FileDropzone
              tool={tool}
              onFilesSelected={handleFilesSelected}
              compact={hasFiles}
              className="mb-6"
            />

            {hasFiles && (
              <>
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <FileText className="w-5 h-5 text-tool-convert" />
                    <span className="font-medium">
                      {files.length} file{files.length !== 1 ? 's' : ''} to convert
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

                <FileList files={files} onRemove={removeFile} className="mb-6" />

                <div className="p-4 rounded-lg bg-tool-convert/5 border border-tool-convert/20 mb-6">
                  <p className="text-sm text-center">
                    Converting to <strong className="text-tool-convert">.{outputFormat.toUpperCase()}</strong> format
                  </p>
                </div>

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
                    className="flex-1 bg-tool-convert hover:bg-tool-convert/90"
                    onClick={handleConvert}
                    disabled={!canProceed}
                  >
                    Convert {files.length} file{files.length !== 1 ? 's' : ''}
                  </Button>
                </div>
              </>
            )}
          </>
        )}

        {job && (
          <div className="space-y-6">
            <JobProgress
              job={job}
              onCancel={cancelJob}
              onRetry={retryJob}
              onDownload={() => {
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
