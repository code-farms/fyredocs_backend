import { useState } from 'react';
import { TOOLS } from '@/config/tools';
import { useFileUpload } from '@/hooks/useFileUpload';
import { useJob } from '@/hooks/useJob';
import { CompressOptions } from '@/types';
import { ToolPageLayout } from './ToolPageLayout';
import { FileDropzone } from '@/components/common/FileDropzone';
import { FileList } from '@/components/common/FileList';
import { JobProgress } from '@/components/common/JobProgress';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { toast } from 'sonner';
import { Minimize2, Trash2, Plus } from 'lucide-react';
import { cn } from '@/lib/utils';

const tool = TOOLS.compress;

type CompressionLevel = 'low' | 'medium' | 'high' | 'extreme';

const compressionLevels = [
  {
    value: 'low',
    label: 'Low',
    description: 'Minimal compression, highest quality',
    reduction: '~10-20%',
  },
  {
    value: 'medium',
    label: 'Medium',
    description: 'Balanced compression and quality',
    reduction: '~30-50%',
  },
  {
    value: 'high',
    label: 'High',
    description: 'Strong compression, good quality',
    reduction: '~50-70%',
  },
  {
    value: 'extreme',
    label: 'Extreme',
    description: 'Maximum compression, reduced quality',
    reduction: '~70-90%',
  },
];

export const CompressTool = () => {
  const [quality, setQuality] = useState<CompressionLevel>('medium');

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
      toast.success('PDF compressed successfully!');
    },
  });

  const handleFilesSelected = (selectedFiles: File[]) => {
    addFiles(selectedFiles);
  };

  const handleCompress = () => {
    if (files.length === 0) {
      toast.error('Please add PDF files to compress');
      return;
    }

    const options: CompressOptions = { quality };

    createJob(
      tool.id,
      files.map((f) => f.id),
      options
    );
  };

  const handleStartOver = () => {
    resetJob();
    clearFiles();
    setQuality('medium');
  };

  const hasFiles = files.length > 0;
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
              compact={hasFiles}
              className="mb-6"
            />

            {hasFiles && (
              <>
                {/* File list header */}
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <Minimize2 className="w-5 h-5 text-tool-compress" />
                    <span className="font-medium">
                      {files.length} file{files.length !== 1 ? 's' : ''} to compress
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

                <FileList files={files} onRemove={removeFile} className="mb-8" />

                {/* Compression options */}
                <div className="p-6 rounded-xl border bg-card mb-6">
                  <h3 className="font-semibold mb-4">Compression Level</h3>

                  <RadioGroup
                    value={quality}
                    onValueChange={(value) => setQuality(value as CompressionLevel)}
                    className="grid grid-cols-2 md:grid-cols-4 gap-3"
                  >
                    {compressionLevels.map((level) => (
                      <div key={level.value}>
                        <RadioGroupItem
                          value={level.value}
                          id={level.value}
                          className="peer sr-only"
                        />
                        <Label
                          htmlFor={level.value}
                          className={cn(
                            'flex flex-col items-center p-4 rounded-lg border-2 cursor-pointer transition-all',
                            'hover:border-primary/50',
                            'peer-data-[state=checked]:border-tool-compress peer-data-[state=checked]:bg-tool-compress/5'
                          )}
                        >
                          <span className="font-medium mb-1">{level.label}</span>
                          <span className="text-xs text-muted-foreground text-center">
                            {level.reduction}
                          </span>
                        </Label>
                      </div>
                    ))}
                  </RadioGroup>

                  <p className="text-sm text-muted-foreground mt-4 text-center">
                    {compressionLevels.find((l) => l.value === quality)?.description}
                  </p>
                </div>

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
                    className="flex-1 bg-tool-compress hover:bg-tool-compress/90"
                    onClick={handleCompress}
                    disabled={!canProceed}
                  >
                    <Minimize2 className="w-4 h-4 mr-2" />
                    Compress {files.length} PDF{files.length !== 1 ? 's' : ''}
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
