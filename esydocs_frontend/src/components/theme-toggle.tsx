import { Button } from "@/components/ui/button";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/hooks/use-theme";

export function ThemeToggle() {
  const [theme, toggleTheme] = useTheme();

  return (
    <Button
      variant="ghost"
      size="sm"
      className="gap-2"
      onClick={toggleTheme}
      aria-label="Toggle theme"
    >
      {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
      <span className="hidden sm:inline">{theme === "dark" ? "Light mode" : "Dark mode"}</span>
    </Button>
  );
}
