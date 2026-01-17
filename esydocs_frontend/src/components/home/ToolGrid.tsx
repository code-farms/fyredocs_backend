import { getAllTools } from '@/config/tools';
import { ToolCard } from './ToolCard';
import { ToolCategory } from '@/types';

interface ToolGridProps {
  category?: ToolCategory;
  limit?: number;
}

export const ToolGrid = ({ category, limit }: ToolGridProps) => {
  let tools = getAllTools();

  if (category) {
    tools = tools.filter((tool) => tool.category === category);
  }

  if (limit) {
    tools = tools.slice(0, limit);
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4 md:gap-6">
      {tools.map((tool) => (
        <ToolCard key={tool.id} tool={tool} />
      ))}
    </div>
  );
};
