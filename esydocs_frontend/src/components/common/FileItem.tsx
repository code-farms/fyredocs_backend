import { FileUpload } from '@/types';
import {
  File,
  X,
  Pause,
  Play,
  RotateCcw,
  ChevronUp,
  ChevronDown,
  CheckCircle2,
  AlertCircle,
  Loader2,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Progress } from '@/components/ui/progress';
import { cn } from '@/lib/utils';

interface FileItemProps {
  file: FileUpload;
  index: number;
  onRemove: () => void;
  onRetry?: () => void;
  onPause?: () => void;
  onResume?: () => void;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  showReorder?: boolean;
}

const formatFileSize = (bytes: number): string => {
  if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${bytes} B`;
};

const stateIcons = {
  idle: null,
  uploading: <Loader2 className="w-4 h-4 animate-spin text-primary" />,
  paused: <Pause className="w-4 h-4 text-muted-foreground" />,
  completed: <CheckCircle2 className="w-4 h-4 text-upload-success" />,
  failed: <AlertCircle className="w-4 h-4 text-upload-error" />,
};

export const FileItem = ({
  file,
  index,
  onRemove,
  onRetry,
  onPause,
  onResume,
  onMoveUp,
  onMoveDown,
  showReorder = false,
}: FileItemProps) => {
  const { state, progress, error } = file;

  return (
    <div
      className={cn(
        'file-item group flex items-center gap-3 p-3 rounded-lg border bg-card transition-all',
        state === 'failed' && 'border-destructive/50 bg-destructive/5',
        state === 'completed' && 'border-upload-success/30 bg-upload-success/5',
        state === 'uploading' && 'border-primary/30'
      )}
    >
      {/* Reorder buttons */}
      {showReorder && (
        <div className="flex flex-col gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
          <Button
            variant="ghost"
            size="icon"
            className="h-5 w-5"
            onClick={onMoveUp}
            disabled={!onMoveUp}
          >
            <ChevronUp className="h-3 w-3" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-5 w-5"
            onClick={onMoveDown}
            disabled={!onMoveDown}
          >
            <ChevronDown className="h-3 w-3" />
          </Button>
        </div>
      )}

      {/* File icon */}
      <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
        <File className="w-5 h-5 text-muted-foreground" />
      </div>

      {/* File info */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium truncate">{file.file.name}</span>
          {stateIcons[state]}
        </div>

        <div className="flex items-center gap-2 mt-1">
          <span className="text-xs text-muted-foreground">
            {formatFileSize(file.file.size)}
          </span>

          {state === 'uploading' && (
            <>
              <span className="text-xs text-muted-foreground">•</span>
              <span className="text-xs text-primary font-medium">
                {progress.percentage}%
              </span>
            </>
          )}

          {state === 'failed' && error && (
            <>
              <span className="text-xs text-muted-foreground">•</span>
              <span className="text-xs text-destructive">{error}</span>
            </>
          )}
        </div>

        {/* Progress bar */}
        {state === 'uploading' && (
          <Progress value={progress.percentage} className="h-1 mt-2" />
        )}
      </div>

      {/* Actions */}
      <div className="flex items-center gap-1">
        {state === 'uploading' && onPause && (
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onPause}>
            <Pause className="h-4 w-4" />
          </Button>
        )}

        {state === 'paused' && onResume && (
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onResume}>
            <Play className="h-4 w-4" />
          </Button>
        )}

        {state === 'failed' && onRetry && (
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onRetry}>
            <RotateCcw className="h-4 w-4" />
          </Button>
        )}

        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8 text-muted-foreground hover:text-destructive"
          onClick={onRemove}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
};
