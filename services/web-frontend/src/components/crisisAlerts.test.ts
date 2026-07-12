import { describe, it, expect } from "vitest";
import { crisisAlertId, reduceCrisisAlerts, type CrisisAlert } from "./crisisAlerts";

function alert(id: string): CrisisAlert {
  return { id, description: "d", occurredAt: "t", siteId: "s" };
}

describe("crisisAlertId (#97)", () => {
  it("keys on incidentId when present", () => {
    expect(crisisAlertId({ incidentId: 42 }, 1)).toBe("incident:42");
    expect(crisisAlertId({ incidentId: "abc" }, 1)).toBe("incident:abc");
  });

  it("falls back to the local sequence when incidentId is absent/empty", () => {
    expect(crisisAlertId({}, 7)).toBe("local:7");
    expect(crisisAlertId({ incidentId: "" }, 8)).toBe("local:8");
    expect(crisisAlertId({ incidentId: null }, 9)).toBe("local:9");
  });

  it("gives distinct ids for same-millisecond alerts without incidentId", () => {
    expect(crisisAlertId({}, 1)).not.toBe(crisisAlertId({}, 2));
  });
});

describe("reduceCrisisAlerts (#97)", () => {
  it("prepends a new alert", () => {
    const next = reduceCrisisAlerts([alert("incident:1")], alert("incident:2"), new Set());
    expect(next.map((a) => a.id)).toEqual(["incident:2", "incident:1"]);
  });

  it("dedupes a re-sent incident", () => {
    const prev = [alert("incident:1")];
    const next = reduceCrisisAlerts(prev, alert("incident:1"), new Set());
    expect(next).toBe(prev); // unchanged reference
  });

  it("does not resurrect a dismissed alert", () => {
    const prev: CrisisAlert[] = [];
    const dismissed = new Set(["incident:1"]);
    const next = reduceCrisisAlerts(prev, alert("incident:1"), dismissed);
    expect(next).toBe(prev);
  });
});
