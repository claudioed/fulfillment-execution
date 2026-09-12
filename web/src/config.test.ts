import { describe, expect, it } from "vitest";
import { resolveFulfillmentApiBase } from "./config";

describe("resolveFulfillmentApiBase", () => {
  it("builds the production API base from the runtime API origin", () => {
    expect(resolveFulfillmentApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/fulfillment-execution",
    );
  });

  it("normalizes a trailing slash on the runtime API origin", () => {
    expect(resolveFulfillmentApiBase({ apiOrigin: "https://warehouse.example/" }, true)).toBe(
      "https://warehouse.example/api/fulfillment-execution",
    );
  });

  it("fails loudly when production runtime configuration has no API origin", () => {
    expect(() => resolveFulfillmentApiBase({}, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
  });

  it("retains the existing standalone API origin in Vite development", () => {
    expect(resolveFulfillmentApiBase({}, false)).toBe("http://localhost:8084");
  });
});
