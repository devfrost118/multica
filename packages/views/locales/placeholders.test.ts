// @vitest-environment node
import { describe, expect, it } from "vitest";
import { RESOURCES } from "./index";

type Json = Record<string, unknown>;

function flatten(obj: unknown, prefix = ""): Record<string, string> {
  if (obj === null || typeof obj !== "object") {
    return typeof obj === "string" && prefix ? { [prefix]: obj } : {};
  }
  return Object.entries(obj as Json).reduce<Record<string, string>>(
    (acc, [key, value]) => {
      const path = prefix ? `${prefix}.${key}` : key;
      return { ...acc, ...flatten(value, path) };
    },
    {},
  );
}

function placeholders(value: string): string[] {
  return [...new Set([...value.matchAll(/\{\{(\w+)\}\}/g)].map((match) => match[1]!))].sort();
}

function pluralStem(key: string): string | null {
  const match = key.match(/^(.*)_(zero|one|two|few|many|other)$/);
  return match ? match[1]! : null;
}

function interpolate(
  template: string,
  vars: Record<string, string | number>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (_match, name: string) =>
    vars[name] === undefined ? `{{${name}}}` : String(vars[name]),
  );
}

function ruPluralSuffix(count: number): "one" | "few" | "many" {
  const mod10 = count % 10;
  const mod100 = count % 100;
  if (mod10 === 1 && mod100 !== 11) return "one";
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return "few";
  return "many";
}

function enReferenceKey(enKeys: Set<string>, key: string): string | null {
  if (enKeys.has(key)) return key;
  const stem = pluralStem(key);
  if (!stem) return null;
  const suffix = key.slice(stem.length + 1);
  if (suffix === "few" || suffix === "many" || suffix === "two") {
    const other = `${stem}_other`;
    return enKeys.has(other) ? other : null;
  }
  return null;
}

describe("locale placeholder contracts", () => {
  const en = RESOURCES.en;

  for (const locale of Object.keys(RESOURCES).filter((item) => item !== "en")) {
    const bundle = RESOURCES[locale as keyof typeof RESOURCES];

    it(`${locale} keeps the same placeholder names as EN, including RU/FR plural siblings`, () => {
      const mismatches: string[] = [];
      for (const ns of Object.keys(en)) {
        const enFlat = flatten(en[ns] ?? {});
        const locFlat = flatten(bundle[ns as keyof typeof bundle] ?? {});
        const enKeys = new Set(Object.keys(enFlat));
        for (const [key, value] of Object.entries(locFlat)) {
          const refKey = enReferenceKey(enKeys, key);
          if (!refKey) continue;
          const expected = placeholders(enFlat[refKey]!);
          const actual = placeholders(value);
          if (expected.join() !== actual.join()) {
            mismatches.push(
              `${ns}.${key}: en=${JSON.stringify(expected)} ${locale}=${JSON.stringify(actual)}`,
            );
          }
        }
      }
      expect(mismatches).toEqual([]);
    });
  }

  it("interpolates RU delete/activity counts for 1, 2, and 5", () => {
    const cases: Array<{
      ns: "autopilots" | "issues" | "skills";
      key: string;
      extra: Record<string, string | number>;
    }> = [
      {
        ns: "autopilots",
        key: "actions.delete_dialog.description",
        extra: { name: "Nightly" },
      },
      { ns: "issues", key: "activity.task_completed", extra: {} },
      { ns: "issues", key: "activity.task_failed", extra: {} },
      { ns: "skills", key: "actions.delete_dialog_desc", extra: {} },
    ];

    for (const item of cases) {
      const flat = flatten(RESOURCES.ru[item.ns]);
      for (const count of [1, 2, 5]) {
        const suffix = ruPluralSuffix(count);
        const template = flat[`${item.key}_${suffix}`];
        expect(template, `${item.ns}:${item.key}_${suffix}`).toBeTruthy();
        const vars: Record<string, string | number> = { count, ...item.extra };
        const resolved = interpolate(template!, vars);
        expect(resolved, `${item.ns}:${item.key}:${count}`).not.toContain("{{");
        if (count === 1 && item.ns === "autopilots") {
          expect(resolved).toContain("Nightly");
        } else if (count !== 1) {
          expect(resolved, `${item.ns}:${item.key}:${count}`).toContain(
            String(count),
          );
        }
        expect(resolved.toLowerCase(), `${item.ns}:${item.key}`).not.toMatch(
          /\b(task|tasks|skill|skills)\b/,
        );
      }
    }
  });

  it("keeps the FR 1.3 placeholders aligned with rematched EN", () => {
    const frIssues = flatten(RESOURCES.fr.issues);
    const frSkills = flatten(RESOURCES.fr.skills);
    const frSettings = flatten(RESOURCES.fr.settings);
    const enSkills = flatten(RESOURCES.en.skills);
    const viewAllOne = interpolate(frIssues["deliverables.view_all_one"]!, {
      count: 1,
    });
    expect(viewAllOne).toContain("1");
    expect(viewAllOne).not.toContain("{{");
    const hintEn = enSkills["detail.overview.description_hint"] ?? "";
    const hint = interpolate(frSkills["detail.overview.description_hint"]!, {
      count: 42,
    });
    if (hintEn.includes("{{count}}")) {
      expect(hint).toContain("42");
    } else {
      expect(hint).not.toContain("{{");
    }
    const autoLink = interpolate(
      frSettings["github.feature_auto_link_description"]!,
      { example: "FRO-329" },
    );
    expect(autoLink).toContain("FRO-329");
    expect(autoLink).not.toContain("{{");
  });
});
