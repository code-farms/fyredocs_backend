import {
  Layers,
  Scissors,
  Minimize2,
  FileText,
  File,
  Table,
  Image,
  FileImage,
  ArrowUpDown,
  RotateCw,
  ScanText,
  Stamp,
  Lock,
  LucideIcon,
} from 'lucide-react';
import { ToolId, ToolCategory } from '@/types';
import { cn } from '@/lib/utils';

const iconMap: Record<string, LucideIcon> = {
  layers: Layers,
  scissors: Scissors,
  'minimize-2': Minimize2,
  'file-text': FileText,
  file: File,
  table: Table,
  image: Image,
  'file-image': FileImage,
  'arrow-up-down': ArrowUpDown,
  'rotate-cw': RotateCw,
  'scan-text': ScanText,
  stamp: Stamp,
  lock: Lock,
  'file-spreadsheet': Table,
};

interface ToolIconProps {
  icon: string;
  category: ToolCategory;
  size?: 'sm' | 'md' | 'lg' | 'xl';
  className?: string;
}

const sizeClasses = {
  sm: 'w-4 h-4',
  md: 'w-5 h-5',
  lg: 'w-6 h-6',
  xl: 'w-8 h-8',
};

const categoryColors: Record<ToolCategory, string> = {
  merge: 'text-tool-merge',
  split: 'text-tool-split',
  compress: 'text-tool-compress',
  convert: 'text-tool-convert',
  organize: 'text-tool-organize',
  security: 'text-tool-security',
  ocr: 'text-tool-ocr',
  watermark: 'text-tool-watermark',
};

export const ToolIcon = ({ icon, category, size = 'md', className }: ToolIconProps) => {
  const IconComponent = iconMap[icon] || File;

  return (
    <IconComponent
      className={cn(sizeClasses[size], categoryColors[category], className)}
    />
  );
};
