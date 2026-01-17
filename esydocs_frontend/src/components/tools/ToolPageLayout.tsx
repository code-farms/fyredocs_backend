import { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { ChevronLeft } from 'lucide-react';
import { ToolDefinition } from '@/types';
import { ToolIcon } from '@/components/icons/ToolIcon';
import { Layout } from '@/components/layout/Layout';
import { cn } from '@/lib/utils';

interface ToolPageLayoutProps {
  tool: ToolDefinition;
  children: ReactNode;
  className?: string;
}

const categoryHeaderClasses: Record<string, string> = {
  merge: 'from-tool-merge/10 to-transparent',
  split: 'from-tool-split/10 to-transparent',
  compress: 'from-tool-compress/10 to-transparent',
  convert: 'from-tool-convert/10 to-transparent',
  organize: 'from-tool-organize/10 to-transparent',
  security: 'from-tool-security/10 to-transparent',
  ocr: 'from-tool-ocr/10 to-transparent',
  watermark: 'from-tool-watermark/10 to-transparent',
};

export const ToolPageLayout = ({
  tool,
  children,
  className,
}: ToolPageLayoutProps) => {
  return (
    <Layout showFooter={false}>
      {/* Tool header */}
      <div
        className={cn(
          'bg-gradient-to-b py-8 md:py-12',
          categoryHeaderClasses[tool.category]
        )}
      >
        <div className="container">
          {/* Back link */}
          <Link
            to="/"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground mb-6 transition-colors"
          >
            <ChevronLeft className="w-4 h-4" />
            All tools
          </Link>

          {/* Tool info */}
          <div className="flex items-start gap-4">
            <div
              className={cn(
                'w-16 h-16 rounded-2xl flex items-center justify-center',
                `bg-tool-${tool.category}/10`
              )}
            >
              <ToolIcon icon={tool.icon} category={tool.category} size="xl" />
            </div>
            <div>
              <h1 className="text-2xl md:text-3xl font-bold mb-2">{tool.name}</h1>
              <p className="text-muted-foreground max-w-xl">{tool.description}</p>
            </div>
          </div>
        </div>
      </div>

      {/* Tool content */}
      <div className={cn('container py-8', className)}>{children}</div>
    </Layout>
  );
};
