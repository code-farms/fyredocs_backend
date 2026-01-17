import { FileUpload } from '@/types';
import { FileItem } from './FileItem';
import { cn } from '@/lib/utils';

interface FileListProps {
  files: FileUpload[];
  onRemove: (fileId: string) => void;
  onRetry?: (fileId: string) => void;
  onPause?: (fileId: string) => void;
  onResume?: (fileId: string) => void;
  onReorder?: (fromIndex: number, toIndex: number) => void;
  showReorder?: boolean;
  className?: string;
}

export const FileList = ({
  files,
  onRemove,
  onRetry,
  onPause,
  onResume,
  onReorder,
  showReorder = false,
  className,
}: FileListProps) => {
  if (files.length === 0) return null;

  return (
    <div className={cn('space-y-2', className)}>
      {files.map((file, index) => (
        <FileItem
          key={file.id}
          file={file}
          index={index}
          onRemove={() => onRemove(file.id)}
          onRetry={onRetry ? () => onRetry(file.id) : undefined}
          onPause={onPause ? () => onPause(file.id) : undefined}
          onResume={onResume ? () => onResume(file.id) : undefined}
          showReorder={showReorder && files.length > 1}
          onMoveUp={
            showReorder && index > 0 && onReorder
              ? () => onReorder(index, index - 1)
              : undefined
          }
          onMoveDown={
            showReorder && index < files.length - 1 && onReorder
              ? () => onReorder(index, index + 1)
              : undefined
          }
        />
      ))}
    </div>
  );
};
