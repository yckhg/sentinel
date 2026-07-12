import { describe, it, expect } from "vitest";
import { reduceCrisisAlerts } from "./crisisAlerts";

// Matches the banner's incidentId-keyed model (①). The reducer is generic over
// `{ incidentId: string }`, so this minimal shape exercises the wired path.
type Alert = { incidentId: string; description: string };

function alert(incidentId: string): Alert {
  return { incidentId, description: "d" };
}

describe("reduceCrisisAlerts (#97 — wired single reducer)", () => {
  it("prepends a new alert", () => {
    const next = reduceCrisisAlerts([alert("1")], alert("2"), new Set());
    expect(next.map((a) => a.incidentId)).toEqual(["2", "1"]);
  });

  it("dedupes a re-sent incidentId (unchanged reference)", () => {
    const prev = [alert("1")];
    const next = reduceCrisisAlerts(prev, alert("1"), new Set());
    expect(next).toBe(prev);
  });

  it("does not add an incidentId in the dismissed set (unchanged reference)", () => {
    const prev: Alert[] = [];
    const dismissed = new Set(["1"]);
    const next = reduceCrisisAlerts(prev, alert("1"), dismissed);
    expect(next).toBe(prev);
  });

  it("still adds a genuinely new incidentId even when others are dismissed", () => {
    const dismissed = new Set(["1"]);
    const next = reduceCrisisAlerts([], alert("2"), dismissed);
    expect(next.map((a) => a.incidentId)).toEqual(["2"]);
  });
});
