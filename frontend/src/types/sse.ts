export interface SSEStreamContext<Value> {
  signal: AbortSignal;
  lastEventId: number | null;
  onOpen: () => void;
  onMessage: (value: Value) => void;
  onRetry: (milliseconds: number) => void;
}
