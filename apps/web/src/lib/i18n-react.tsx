import { createContext, useContext, useMemo, type ReactNode } from "react";
import {
  createTranslator,
  formatDate,
  formatRelativeDate,
  normalizeLocale,
  type AppLocale,
  type Translator,
} from "./i18n";

interface I18nContextValue {
  locale: AppLocale;
  t: Translator;
  formatDate: (
    value: string | Date,
    options: Intl.DateTimeFormatOptions,
  ) => string;
  formatDateTime: (value: string | Date) => string;
  formatRelativeDate: (value: string) => string;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({
  locale,
  children,
}: {
  locale: string | null | undefined;
  children: ReactNode;
}) {
  const normalized = normalizeLocale(locale);
  const value = useMemo<I18nContextValue>(() => {
    const t = createTranslator(normalized);
    return {
      locale: normalized,
      t,
      formatDate: (value, options) => formatDate(value, normalized, options),
      formatDateTime: (value) =>
        formatDate(value, normalized, {
          month: "numeric",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        }),
      formatRelativeDate: (value) => formatRelativeDate(value, normalized, t),
    };
  }, [normalized]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const value = useContext(I18nContext);
  if (!value) {
    throw new Error("useI18n must be used inside I18nProvider");
  }
  return value;
}
