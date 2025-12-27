import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface ProtectOptionsProps {
  onOptionsChange: (options: any) => void;
}

export default function ProtectOptions({ onOptionsChange }: ProtectOptionsProps) {
  const [password, setPassword] = useState("");

  const handleApply = () => {
    onOptionsChange({ password: password });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Protect PDF Options</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="space-y-4">
          <div>
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              placeholder="Enter password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="text-sm text-muted-foreground mt-1">
              Set a password to protect your PDF file.
            </p>
          </div>
          <Button onClick={handleApply}>Apply</Button>
        </div>
      </CardContent>
    </Card>
  );
}
