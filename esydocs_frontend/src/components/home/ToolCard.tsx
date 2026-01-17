import { Link } from 'react-router-dom';
import { ToolDefinition } from '@/types';
import { ToolIcon } from '@/components/icons/ToolIcon';
import { cn } from '@/lib/utils';
import { ArrowRight } from 'lucide-react';

interface ToolCardProps {
  tool: ToolDefinition;
  className?: string;
}

const categoryBgClasses: Record<string, string> = {
  merge: 'bg-tool-merge-light group-hover:bg-tool-merge/10',
  split: 'bg-tool-split-light group-hover:bg-tool-split/10',
  compress: 'bg-tool-compress-light group-hover:bg-tool-compress/10',
  convert: 'bg-tool-convert-light group-hover:bg-tool-convert/10',
  organize: 'bg-tool-organize-light group-hover:bg-tool-organize/10',
  security: 'bg-tool-security-light group-hover:bg-tool-security/10',
  ocr: 'bg-tool-ocr-light group-hover:bg-tool-ocr/10',
  watermark: 'bg-tool-watermark-light group-hover:bg-tool-watermark/10',
};

const categoryIconBgClasses: Record<string, string> = {
  merge: 'bg-tool-merge/10 group-hover:bg-tool-merge/20',
  split: 'bg-tool-split/10 group-hover:bg-tool-split/20',
  compress: 'bg-tool-compress/10 group-hover:bg-tool-compress/20',
  convert: 'bg-tool-convert/10 group-hover:bg-tool-convert/20',
  organize: 'bg-tool-organize/10 group-hover:bg-tool-organize/20',
  security: 'bg-tool-security/10 group-hover:bg-tool-security/20',
  ocr: 'bg-tool-ocr/10 group-hover:bg-tool-ocr/20',
  watermark: 'bg-tool-watermark/10 group-hover:bg-tool-watermark/20',
};

export const ToolCard = ({ tool, className }: ToolCardProps) => {
  return (
    <Link
      to={tool.route}
      className={cn(
        'tool-card group block p-6 rounded-2xl border bg-card shadow-sm hover:shadow-tool transition-all duration-300',
        className
      )}
    >
      {/* Icon */}
      <div
        className={cn(
          'w-14 h-14 rounded-xl flex items-center justify-center mb-4 transition-colors duration-300',
          categoryIconBgClasses[tool.category]
        )}
      >
        <ToolIcon icon={tool.icon} category={tool.category} size="lg" />
      </div>

      {/* Content */}
      <h3 className="font-semibold text-lg mb-2 group-hover:text-primary transition-colors">
        {tool.name}
      </h3>
      <p className="text-sm text-muted-foreground line-clamp-2 mb-4">
        {tool.description}
      </p>

      {/* Action hint */}
      <div className="flex items-center gap-2 text-sm font-medium text-primary opacity-0 group-hover:opacity-100 transition-opacity">
        <span>Use tool</span>
        <ArrowRight className="w-4 h-4 transition-transform group-hover:translate-x-1" />
      </div>
    </Link>
  );
};
