import { Component, type ReactNode } from "react";
import { Button } from "./ui/button";

export class PageBoundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    if (this.state.failed)
      return (
        <div className="empty-state" role="alert">
          <span>页面加载失败</span>
          <Button
            size="sm"
            variant="outline"
            onClick={() => window.location.reload()}
          >
            重新加载
          </Button>
        </div>
      );
    return this.props.children;
  }
}
