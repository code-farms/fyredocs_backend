import { useState } from 'react';
import { TOOLS } from '@/config/tools';
import { useFileUpload } from '@/hooks/useFileUpload';
import { useJob } from '@/hooks/useJob';
import { SplitOptions } from '@/types';
import { ToolPageLayout } from './ToolPageLayout';
import { FileDropzone } from '@/components/common/FileDropzone';
import { FileList } from '@/components/common/FileList';
import { JobProgress } from '@/components/common/JobProgress';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { toast } from 'sonner';
import { Scissors, FileText } from 'lucide-react';

const tool = TOOLS.split;

type SplitMode = 'all' | 'range' | 'extract';

export const SplitTool = () => {
  const [splitMode, setSplitMode] = useState<SplitMode>('all');
  const [rangeInput, setRangeInput] = useState('');

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
      toast.success('PDF split successfully!');
    },
  });

  const handleFilesSelected = (selectedFiles: File[]) => {
    addFiles(selectedFiles);
  };

  const handleSplit = () => {
    if (files.length === 0) {
      toast.error('Please add a PDF file to split');
      return;
    }

    const options: SplitOptions = {
      mode: splitMode,
      ranges: splitMode === 'range' ? rangeInput : undefined,
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
    setSplitMode('all');
    setRangeInput('');
  };

  const hasFile = files.length > 0;
  const isComplete = job?.state === 'completed';

  return (
    <ToolPageLayout tool={tool}>
      <div className="max-w-3xl mx-auto">
        {/* Upload section */}
        {!job && (
          <>
            {!hasFile ? (
              <FileDropzone
                tool={tool}
                onFilesSelected={handleFilesSelected}
                className="mb-6"
              />
            ) : (
              <>
                {/* File preview */}
                <div className="mb-8">
                  <FileList files={files} onRemove={removeFile} />
                </div>

                {/* Split options */}
                <div className="p-6 rounded-xl border bg-card mb-6">
                  <h3 className="font-semibold mb-4 flex items-center gap-2">
                    <Scissors className="w-5 h-5 text-tool-split" />
                    Split Options
                  </h3>

                  <RadioGroup
                    value={splitMode}
                    onValueChange={(value) => setSplitMode(value as SplitMode)}
                    className="space-y-4"
                  >
                    <div className="flex items-start space-x-3 p-4 rounded-lg border hover:border-primary/50 transition-colors cursor-pointer">
                      <RadioGroupItem value="all" id="all" className="mt-0.5" />
                      <div className="flex-1">
                        <Label htmlFor="all" className="font-medium cursor-pointer">
                          Split all pages
                        </Label>
                        <p className="text-sm text-muted-foreground">
                          Extract each page as a separate PDF file
                        </p>
                      </div>
                    </div>

                    <div className="flex items-start space-x-3 p-4 rounded-lg border hover:border-primary/50 transition-colors cursor-pointer">
                      <RadioGroupItem value="range" id="range" className="mt-0.5" />
                      <div className="flex-1">
                        <Label htmlFor="range" className="font-medium cursor-pointer">
                          Split by range
                        </Label>
                        <p className="text-sm text-muted-foreground mb-3">
                          Specify page ranges to extract
                        </p>
                        {splitMode === 'range' && (
                          <Input
                            placeholder="e.g., 1-3, 5, 7-10"
                            value={rangeInput}
                            onChange={(e) => setRangeInput(e.target.value)}
                            className="max-w-xs"
                          />
                        )}
                      </div>
                    </div>

                    <div className="flex items-start space-x-3 p-4 rounded-lg border hover:border-primary/50 transition-colors cursor-pointer">
                      <RadioGroupItem value="extract" id="extract" className="mt-0.5" />
                      <div className="flex-1">
                        <Label htmlFor="extract" className="font-medium cursor-pointer">
                          Extract every N pages
                        </Label>
                        <p className="text-sm text-muted-foreground">
                          Split into multiple PDFs with fixed page count each
                        </p>
                      </div>
                    </div>
                  </RadioGroup>
                </div>

                {/* Action buttons */}
                <div className="flex gap-3">
                  <Button
                    variant="outline"
                    className="flex-1"
                    onClick={clearFiles}
                  >
                    Choose different file
                  </Button>
                  <Button
                    className="flex-1 bg-tool-split hover:bg-tool-split/90"
                    onClick={handleSplit}
                    disabled={!canProceed || (splitMode === 'range' && !rangeInput)}
                  >
                    <Scissors className="w-4 h-4 mr-2" />
                    Split PDF
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
                Start over with new file
              </Button>
            )}
          </div>
        )}
      </div>
    </ToolPageLayout>
  );
};
