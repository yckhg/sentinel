import { Component, type ReactNode, type ErrorInfo } from "react";

interface Props {
  children: ReactNode;
  /** Custom fallback UI. Defaults to a small inline error card. */
  fallback?: ReactNode;
  /** Label for logging which boundary caught the error. */
  label?: string;
}

interface State {
  hasError: boolean;
}

/**
 * Catches render/lifecycle errors in its subtree so one failing page can't blank
 * the whole app (including the crisis banner). Wrap independent regions in their
 * own boundary so they fail in isolation (#99).
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false };

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error(`ErrorBoundary(${this.props.label ?? "root"}) caught:`, error, info);
  }

  handleReset = (): void => {
    this.setState({ hasError: false });
  };

  render(): ReactNode {
    if (this.state.hasError) {
      if (this.props.fallback !== undefined) return this.props.fallback;
      return (
        <div className="error-boundary" role="alert">
          <p className="error-boundary-title">문제가 발생했습니다</p>
          <p className="error-boundary-desc">
            이 영역을 표시하는 중 오류가 발생했습니다.
          </p>
          <button className="mgmt-btn mgmt-btn-secondary" onClick={this.handleReset}>
            다시 시도
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
