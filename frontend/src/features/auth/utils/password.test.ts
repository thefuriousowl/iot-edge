import { describe, expect, it } from "vitest";

import { countPasswordClasses, passwordLength, passwordRequirementStates } from "./password";

describe("password policy helpers", () => {
  it("counts Unicode code points rather than UTF-16 units", () => {
    expect(passwordLength("Aก1!🙂ขค")).toBe(7);
    expect(passwordLength("Aก1!🙂ขคง")).toBe(8);
  });

  it("matches backend Unicode letter, digit, punctuation, and symbol classes", () => {
    expect(countPasswordClasses("Éclair๕€")).toBe(4);
    expect(countPasswordClasses("lowercase-only")).toBe(2);
  });

  it("returns the shared length, complexity, and username guidance", () => {
    expect(passwordRequirementStates("FreshP@ss3", "admin")).toEqual([
      { label: "8–128 characters", valid: true },
      { label: "At least 3 of: uppercase, lowercase, number, special character", valid: true },
      { label: "Does not contain your username", valid: true },
    ]);
    expect(passwordRequirementStates("Admin@123Secure", "ADMIN")[2].valid).toBe(false);
  });
});
