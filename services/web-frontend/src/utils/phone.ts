/**
 * Shared phone-number helpers, extracted from ManagementPage as part of the
 * admin-IA decomposition. The contacts / system leaf subpages import these so
 * the validation regex and the live hyphen formatter live in exactly one place.
 *
 * Kept byte-for-byte equivalent to the original ManagementPage definitions
 * (behavior preservation, invariant ①).
 */

/** Korean mobile number in canonical hyphenated form, e.g. 010-1234-5678. */
export const PHONE_REGEX = /^01[016789]-\d{3,4}-\d{4}$/;

/**
 * Live input formatter: strips non-digits and re-inserts hyphens as the user
 * types (3-4-4 grouping, capped at 11 digits). Mirrors the original
 * ManagementPage `formatPhoneInput`.
 */
export function formatPhoneInput(value: string): string {
  const digits = value.replace(/\D/g, "");
  if (digits.length <= 3) return digits;
  if (digits.length <= 7) return `${digits.slice(0, 3)}-${digits.slice(3)}`;
  return `${digits.slice(0, 3)}-${digits.slice(3, 7)}-${digits.slice(7, 11)}`;
}
