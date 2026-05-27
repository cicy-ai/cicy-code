import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { urls } from '../../config';
import { TokenManager } from '../../services/tokenManager';

const AUDIT_AGENT_PANE = 'w-10000:main.0';

export default function AssistantTab() {
  const { t } = useTranslation('audit');
  const token = TokenManager.getToken() || '';
  const src = useMemo(() => urls.ttydOpen(AUDIT_AGENT_PANE, token), [token]);

  if (!token) {
    return (
      <div data-id="audit-assistant-no-token" className="p-8 text-sm text-[var(--vsc-text-muted)]">
        {t('assistantNoToken')}
      </div>
    );
  }

  return (
    <div data-id="audit-assistant-root" className="h-full w-full flex flex-col">
      <div data-id="audit-assistant-hint" className="px-3 py-2 text-[11px] text-[var(--vsc-text-muted)] border-b border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] shrink-0">
        {t('assistantHeaderBefore')}<code>w-10000</code>{t('assistantHeaderAfter')}
      </div>
      <div data-id="audit-assistant-frame-wrap" className="flex-1 min-h-0">
        <iframe
          data-id="audit-assistant-frame"
          src={src}
          className="h-full w-full border-0 bg-black"
          title="audit-policy-assistant"
        />
      </div>
    </div>
  );
}
