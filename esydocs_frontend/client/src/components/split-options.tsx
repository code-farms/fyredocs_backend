import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface SplitOptionsProps {
  onOptionsChange: (options: any) => void;
}

export default function SplitOptions({ onOptionsChange }: SplitOptionsProps) {
  const [splitRange, setSplitRange] = useState("");

  const handleApply = () => {
    onOptionsChange({ range: splitRange });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Split PDF Options</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="space-y-4">
          <div>
            <Label htmlFor="split-range">Page ranges</Label>
            <Input
              id="split-range"
              placeholder="e.g. 1-3, 5, 7-9"
              value={splitRange}
              onChange={(e) => setSplitRange(e.target.value)}
            />
            <p className="text-sm text-muted-foreground mt-1">
              Enter page numbers or ranges separated by commas.
            </p>
          </div>
          <Button onClick={handleApply}>Apply</Button>
        </div>
      </CardContent>
    </Card>
  );
}
