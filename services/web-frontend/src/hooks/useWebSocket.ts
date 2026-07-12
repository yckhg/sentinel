import { useEffect, useRef, useCallback, useState } from "react";

interface WSMessage {
  type: string;
  payload: Record<string, unknown>;
  timestamp: string;
}

type MessageHandler = (msg: WSMessage) => void;
type OpenHandler = () => void;

export type WsState = "connected" | "disconnected" | "reconnecting";

interface UseWebSocketReturn {
  connected: boolean;
  wsState: WsState;
}

/**
 * Single app-root WebSocket with exponential backoff auto-reconnect.
 *
 * `wsState` reflects the SOCKET state ONLY (spec web-frontend §출력 3-(a), 단언 O):
 * on socket open it returns to `connected` immediately, decoupled from any
 * backfill success/failure. During backoff it is `reconnecting`; with no token
 * (cannot connect) it is `disconnected`.
 *
 * `onOpen` fires on every successful (re)connect so the caller can run the
 * best-effort reconnect backfill (spec §출력 3-(c), 단언 P) — this is kept
 * separate from `wsState`.
 */
export default function useWebSocket(
  onMessage: MessageHandler,
  onOpen?: OpenHandler
): UseWebSocketReturn {
  const [wsState, setWsState] = useState<WsState>("disconnected");
  const wsRef = useRef<WebSocket | null>(null);
  const retriesRef = useRef(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onMessageRef = useRef(onMessage);
  const onOpenRef = useRef(onOpen);

  onMessageRef.current = onMessage;
  onOpenRef.current = onOpen;

  const connect = useCallback(() => {
    const token = localStorage.getItem("token");
    if (!token) {
      setWsState("disconnected");
      return;
    }

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${window.location.host}/ws?token=${token}`;

    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      // Socket-only state: back to connected immediately, regardless of backfill.
      setWsState("connected");
      retriesRef.current = 0;
      onOpenRef.current?.();
    };

    ws.onmessage = (event) => {
      try {
        const msg: WSMessage = JSON.parse(event.data);
        onMessageRef.current(msg);
      } catch {
        // ignore malformed messages
      }
    };

    ws.onclose = () => {
      wsRef.current = null;
      // We always schedule a reconnect, so surface `reconnecting`.
      setWsState("reconnecting");
      // exponential backoff: 1s, 2s, 4s, 8s max
      const delay = Math.min(1000 * Math.pow(2, retriesRef.current), 8000);
      retriesRef.current++;
      timerRef.current = setTimeout(connect, delay);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, []);

  useEffect(() => {
    connect();
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
      if (wsRef.current) {
        wsRef.current.onclose = null;
        wsRef.current.close();
      }
    };
  }, [connect]);

  return { connected: wsState === "connected", wsState };
}
