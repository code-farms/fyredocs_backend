import { Route, Switch } from "wouter";
import Home from "@/pages/home";
import ToolPage from "@/pages/tool/[toolName]";
import NotFound from "@/pages/not-found";

export function AppRoutes() {
  return (
    <Switch>
      <Route path="/" component={Home} />
      <Route path="/tool/:toolName" component={ToolPage} />
      <Route component={NotFound} />
    </Switch>
  );
}
