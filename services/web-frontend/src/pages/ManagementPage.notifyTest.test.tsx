import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import ManagementPage from "./ManagementPage";

// -----------------------------------------------------------------------------
// TDD GATES for docs/spec/notification-test-send.md — 관리자 UI 어포던스(§출력 13,
// 단언 G·K·D의 클라이언트 표면). These assert the notification test-send UI that
// ManagementPage must add. They are RED until that UI exists.
//
// Contract driven here:
//   - 이메일 / SMS 각각의 독립 "테스트 전송" 컨트롤이 존재한다 (KakaoTalk은 없음).
//   - 요청 시점 설정 판정: GET /api/notifications/channels 로 채널 usable 상태를 읽어,
//     not_configured(usable=false) 채널은 "미설정" 안내 + 컨트롤 비활성/주석 표시
//     (성공한 것처럼 보이게 하지 않음).
//   - 설정된(usable=true) 채널의 테스트 버튼을 누르면
//     POST /api/notifications/test {channel, target} 를 발행한다.
//
// NOTE: HealthPanel(leaf C, page 최상단)은 건드리지 않는다 — 여기서는 알림 테스트
// 섹션만 검증한다.
// -----------------------------------------------------------------------------

function adminToken(): string {
  const b64 = btoa(JSON.stringify({ role: "admin", exp: Math.floor(Date.now() / 1000) + 3600 }))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `h.${b64}.s`;
}

function jsonRes(data: unknown, ok = true, status = 200): Promise<Response> {
  return Promise.resolve({
    ok,
    status,
    json: () => Promise.resolve(data),
    blob: () => Promise.resolve(new Blob()),
  } as unknown as Response);
}

interface ChannelStatus {
  email: { usable: boolean; reason?: string };
  sms: { usable: boolean; reason?: string };
}

const healthSummary = {
  summary: { healthy: 0, abnormal: 0, offline: 0 },
  services: [] as { id: string; status: string }[],
  exceptions: [] as unknown[],
  exceptionsOverflow: 0,
};

// installFetch routes by URL. The notification-channels status is the fixture the
// test-send UI must consume. All other mount-time endpoints return empty/benign
// shapes so ManagementPage renders past its loading state without crashing.
function installFetch(channels: ChannelStatus) {
  const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
    const u = String(input);
    if (u.includes("/api/notifications/channels")) return jsonRes(channels);
    if (u.includes("/api/notifications/test")) return jsonRes({ outcome: "sent" });
    if (u.includes("/api/health/summary")) return jsonRes(healthSummary);
    if (u.includes("/api/health/events")) return jsonRes([]);
    if (u.includes("/api/cameras")) return jsonRes([]);
    if (u.includes("/api/devices")) return jsonRes([]);
    if (u.includes("/api/health")) return jsonRes([]);
    if (u.includes("/api/contacts")) return jsonRes([]);
    if (u.includes("/api/sites")) return jsonRes([]);
    if (u.includes("/api/links")) return jsonRes([]);
    if (u.includes("/api/invitations")) return jsonRes([]);
    if (u.includes("/api/storage")) return jsonRes({ recordingsBytes: 0, archivesBytes: 0, totalUsedBytes: 0, archiveCount: 0 });
    if (u.includes("/api/archives")) return jsonRes([]);
    if (u.includes("/api/settings")) return jsonRes([]);
    if (u.includes("/auth/pending")) return jsonRes([]);
    if (u.includes("/auth/users")) return jsonRes([]);
    return jsonRes({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

// Find the fetch call that issued POST /api/notifications/test and parse its body.
function testSendCall(fetchMock: ReturnType<typeof vi.fn>): { channel?: string; target?: string } | null {
  const call = fetchMock.mock.calls.find((c) => String(c[0]).includes("/api/notifications/test"));
  if (!call) return null;
  const init = call[1] as RequestInit | undefined;
  const raw = typeof init?.body === "string" ? init.body : "";
  try {
    return JSON.parse(raw) as { channel?: string; target?: string };
  } catch {
    return {};
  }
}

const EMAIL_TEST = /(이메일|email).*(테스트|test)|(테스트|test).*(이메일|email)/i;
const SMS_TEST = /(sms|문자|전화|연락처).*(테스트|test)|(테스트|test).*(sms|문자|전화|연락처)/i;

beforeEach(() => {
  localStorage.setItem("token", adminToken());
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

// RETIRED (admin-IA master): ManagementPage was removed from the App render path
// and frozen pending leaf decomposition — its notify-test UI is being relocated to
// pages/admin/NotifyTestPage.tsx (page-notify-test leaf), which will re-own these
// assertions against its own subpage + feature markers. Skipped (not deleted) so
// the frozen source + intended contract stay as a reference for that leaf.
describe.skip("ManagementPage — 채널별 테스트 발송 UI (spec notification-test-send)", () => {
  it("이메일/SMS 독립 '테스트 전송' 컨트롤을 제공하고 KakaoTalk은 제공하지 않는다 (§출력 13·단언 G)", async () => {
    installFetch({ email: { usable: true }, sms: { usable: true } });
    render(<ManagementPage />);
    await screen.findByText(/연락처 관리/);

    const emailBtn = screen.queryByRole("button", { name: EMAIL_TEST });
    const smsBtn = screen.queryByRole("button", { name: SMS_TEST });
    expect(emailBtn).not.toBeNull(); // RED: 이메일 테스트 발송 컨트롤 부재
    expect(smsBtn).not.toBeNull(); // RED: SMS 테스트 발송 컨트롤 부재

    // KakaoTalk은 테스트 채널로 노출되지 않는다 — 검사 범위는 테스트발송 섹션으로
    // 한정한다(단언 G의 UI 의도는 "테스트 채널 집합 = {email, sms}"이지, 무관한
    // 기능의 설명 텍스트에서 KakaoTalk 언급 자체를 금지하는 것이 아니다).
    const section = within(screen.getByTestId("notify-test"));
    expect(section.queryByRole("button", { name: /카카오|kakao/i })).toBeNull();
    expect(section.queryByText(/카카오톡|kakaotalk/i)).toBeNull();
  });

  it("not_configured 채널은 '미설정' 안내와 함께 컨트롤을 비활성/주석한다 (§출력 13·단언 K)", async () => {
    const fetchMock = installFetch({ email: { usable: false, reason: "not_configured" }, sms: { usable: true } });
    render(<ManagementPage />);
    await screen.findByText(/연락처 관리/);

    // 요청 시점 설정 판정을 위해 채널 상태를 조회한다.
    await waitFor(() => {
      expect(fetchMock.mock.calls.some((c) => String(c[0]).includes("/api/notifications/channels"))).toBe(true);
    });

    // 미설정 채널(이메일)에는 "미설정" 안내가 표시된다 (성공처럼 보이지 않음).
    expect(screen.queryByText(/미설정/)).not.toBeNull(); // RED: 안내 부재

    // 그리고 이메일 테스트 컨트롤은 비활성화되어 있다.
    const emailBtn = screen.queryByRole("button", { name: EMAIL_TEST });
    expect(emailBtn).not.toBeNull(); // RED: 컨트롤 부재
    expect((emailBtn as HTMLButtonElement | null)?.disabled).toBe(true); // RED: 비활성 아님
  });

  it("설정된 채널의 테스트 버튼을 누르면 POST /api/notifications/test {channel,target}를 발행한다 (§API 계약 델타)", async () => {
    const fetchMock = installFetch({ email: { usable: true }, sms: { usable: true } });
    render(<ManagementPage />);
    await screen.findByText(/연락처 관리/);

    const smsBtn = screen.queryByRole("button", { name: SMS_TEST });
    expect(smsBtn).not.toBeNull(); // RED: SMS 테스트 컨트롤 부재

    const user = userEvent.setup();
    // 관리자가 그 자리에서 입력하는 명시 단일 대상: 전화번호 입력이 있으면 채운다.
    const boxes = screen.queryAllByRole("textbox");
    if (boxes.length > 0) {
      await user.type(boxes[0]!, "010-1234-5678");
    }
    await user.click(smsBtn as HTMLElement);

    await waitFor(() => {
      const body = testSendCall(fetchMock);
      expect(body).not.toBeNull(); // RED: 엔드포인트 미호출
      expect(body?.channel).toBe("sms");
      expect(typeof body?.target).toBe("string");
    });
  });
});

// Silence unused-import warnings during iteration.
void within;
