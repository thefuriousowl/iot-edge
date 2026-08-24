export interface PasswordRequirementState {
  label: string;
  valid: boolean;
}

export function passwordLength(password: string): number {
  return Array.from(password).length;
}

export function countPasswordClasses(password: string): number {
  return [
    /\p{Lu}/u.test(password),
    /\p{Ll}/u.test(password),
    /\p{Nd}/u.test(password),
    /[\p{P}\p{S}]/u.test(password),
  ].filter(Boolean).length;
}

export function passwordRequirementStates(password: string, username: string): PasswordRequirementState[] {
  const length = passwordLength(password);
  const normalizedUsername = username.trim().toLocaleLowerCase();
  return [
    { label: "8–128 characters", valid: length >= 8 && length <= 128 },
    { label: "At least 3 of: uppercase, lowercase, number, special character", valid: countPasswordClasses(password) >= 3 },
    {
      label: "Does not contain your username",
      valid: normalizedUsername.length > 0 && password.length > 0 && !password.toLocaleLowerCase().includes(normalizedUsername),
    },
  ];
}
