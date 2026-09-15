export const defaultLocale = "en" as const;

export const supportedLocales = [
  {
    code: "en",
    language: "en-US",
    name: "English",
    file: "en.json",
    dir: "ltr" as const,
    flag: "us",
  },
  {
    code: "zh",
    language: "zh-CN",
    name: "简体中文",
    file: "zh.json",
    dir: "ltr" as const,
    flag: "cn",
  },
  { code: "es", language: "es", name: "Español", file: "es.json", dir: "ltr" as const, flag: "es" },
  { code: "fr", language: "fr", name: "Français", file: "fr.json", dir: "ltr" as const, flag: "fr" },
  { code: "de", language: "de", name: "Deutsch", file: "de.json", dir: "ltr" as const, flag: "de" },
  { code: "pt", language: "pt-BR", name: "Português (Brasil)", file: "pt.json", dir: "ltr" as const, flag: "br" },
  { code: "ja", language: "ja", name: "日本語", file: "ja.json", dir: "ltr" as const, flag: "jp" },
  { code: "ko", language: "ko", name: "한국어", file: "ko.json", dir: "ltr" as const, flag: "kr" },
  { code: "ru", language: "ru", name: "Русский", file: "ru.json", dir: "ltr" as const, flag: "ru" },
  { code: "ar", language: "ar", name: "العربية", file: "ar.json", dir: "rtl" as const, flag: "sa" },
  { code: "hi", language: "hi", name: "हिन्दी", file: "hi.json", dir: "ltr" as const, flag: "in" },
  { code: "it", language: "it", name: "Italiano", file: "it.json", dir: "ltr" as const, flag: "it" },
] as const;

export type LocaleCode = (typeof supportedLocales)[number]["code"];

export function isLocaleCode(value: unknown): value is LocaleCode {
  return supportedLocales.some((locale) => locale.code === value);
}
