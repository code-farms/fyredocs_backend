import { cn } from "@/lib/utils";
import { type ToolConfig } from "@/lib/types";
import { useLocation } from "wouter";

interface ToolCardProps {
  tool: ToolConfig;
  className?: string;
}

export default function ToolCard({ tool, className }: ToolCardProps) {
  const [, navigate] = useLocation();

  const handleClick = () => {
    navigate(`/tool/${tool.id}`);
  };

  return (
    <div 
      className={cn(
        "tool-card bg-card rounded-xl p-6 border border-border cursor-pointer",
        className
      )}
      onClick={handleClick}
      data-testid={`tool-card-${tool.id}`}
    >
      <div className={cn("w-12 h-12 rounded-lg flex items-center justify-center mb-4", tool.bgColor)}>
        <i className={cn(tool.icon, tool.iconColor, "text-xl")} />
      </div>
      <h4 className="font-semibold text-foreground mb-2">{tool.name}</h4>
      <p className="text-sm text-muted-foreground">{tool.description}</p>
    </div>
  );
}
