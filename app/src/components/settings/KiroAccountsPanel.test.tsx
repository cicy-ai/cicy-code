import { fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  getKiroAccounts: vi.fn(),
  getGoogleAccounts: vi.fn(),
  getKiroAccountSecret: vi.fn(),
  saveKiroAccount: vi.fn(),
  deleteKiroAccount: vi.fn(),
  getKiroAccountTOTP: vi.fn(),
  importKiroAccounts: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => ({ sendToAgent: vi.fn() }));
vi.mock('./GoogleAccountsPanel', () => ({ openChromeProfile: vi.fn() }));

import KiroAccountsPanel from './KiroAccountsPanel';

beforeEach(() => {
  vi.clearAllMocks();
  api.getKiroAccounts.mockResolvedValue({ data: { accounts: [{ name: 'a@example.com', login_type: 'builder_id', email: 'a@example.com', password_set: true, '2fa_set': true, profile: '', note: '主号' }] } });
  api.getGoogleAccounts.mockResolvedValue({ data: { accounts: [] } });
  api.importKiroAccounts.mockResolvedValue({ data: { added: ['b@example.com'], updated: [], skipped_lines: [] } });
});

describe('KiroAccountsPanel', () => {
  it('lists accounts with their sign-in method and note', async () => {
    const { container } = render(<KiroAccountsPanel active paneId="w-1" />);
    await waitFor(() => expect(container.querySelector('[data-id="kiro-account-type"]')?.textContent).toBe('AWS Builder ID'));
    expect(container.querySelector('[data-id="kiro-account-note"]')?.textContent).toContain('主号');
  });

  it('bulk-imports pasted lines and reloads the list', async () => {
    const { container } = render(<KiroAccountsPanel active paneId="w-1" />);
    await waitFor(() => expect(api.getKiroAccounts).toHaveBeenCalledTimes(1));
    fireEvent.click(container.querySelector('[data-id="kiro-account-import"]')!);
    const text = await waitFor(() => document.querySelector('[data-id="kiro-account-import-text"]') as HTMLTextAreaElement);
    fireEvent.change(text, { target: { value: 'github:b@example.com:pw:SEED' } });
    fireEvent.click(document.querySelector('[data-id="kiro-account-import-submit"]')!);
    await waitFor(() => expect(api.importKiroAccounts).toHaveBeenCalledWith('github:b@example.com:pw:SEED'));
    await waitFor(() => expect(api.getKiroAccounts).toHaveBeenCalledTimes(2));
  });
});
