import { fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  getAwsAccounts: vi.fn(),
  getGoogleAccounts: vi.fn(),
  getAwsAccountSecret: vi.fn(),
  saveAwsAccount: vi.fn(),
  deleteAwsAccount: vi.fn(),
  testAwsAccount: vi.fn(),
  bindAwsAccount: vi.fn(),
  inspectAwsAccount: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => ({ sendToAgent: vi.fn() }));
vi.mock('./GoogleAccountsPanel', () => ({ openChromeProfile: vi.fn() }));

import AwsAccountsPanel from './AwsAccountsPanel';

beforeEach(() => {
  vi.clearAllMocks();
  api.getAwsAccounts.mockResolvedValue({ data: { accounts: [{ name: 'prod', access_key_id: 'AKIAEXAMPLE', secret_set: true, secret_tail: 'WXYZ', region: 'ap-east-1', note: '生产账号' }] } });
  api.getGoogleAccounts.mockResolvedValue({ data: { accounts: [] } });
  api.testAwsAccount.mockResolvedValue({ data: { account_id: '123456789012', arn: 'arn:aws:iam::123456789012:user/prod' } });
  api.saveAwsAccount.mockResolvedValue({ data: { success: true } });
});

describe('AwsAccountsPanel', () => {
  it('lists keys with their note and never the secret', async () => {
    const { container } = render(<AwsAccountsPanel active paneId="w-1" />);
    await waitFor(() => expect(container.querySelector('[data-id="aws-account-note"]')?.textContent).toContain('生产账号'));
    expect(container.textContent).toContain('AKIAEXAMPLE');
    expect(container.textContent).toContain('••••WXYZ');
    expect(container.querySelector('[data-id="aws-account-region"]')?.textContent).toBe('ap-east-1');
  });

  it('saves a new key together with its note', async () => {
    const { container } = render(<AwsAccountsPanel active paneId="w-1" />);
    await waitFor(() => expect(api.getAwsAccounts).toHaveBeenCalled());
    fireEvent.click(container.querySelector('[data-id="aws-account-add"]')!);
    const field = (id: string) => document.querySelector(`[data-id="aws-account-${id}-input"]`) as HTMLInputElement;
    await waitFor(() => expect(field('ak')).toBeTruthy());
    fireEvent.change(field('name'), { target: { value: 'dev' } });
    fireEvent.change(field('ak'), { target: { value: 'AKIADEV' } });
    fireEvent.change(field('secret'), { target: { value: 'devsecret' } });
    fireEvent.change(field('note'), { target: { value: '个人测试' } });
    fireEvent.click(document.querySelector('[data-id="aws-account-save"]')!);
    await waitFor(() => expect(api.saveAwsAccount).toHaveBeenCalled());
    expect(api.saveAwsAccount.mock.calls[0][0]).toMatchObject({ name: 'dev', access_key_id: 'AKIADEV', secret_access_key: 'devsecret', note: '个人测试' });
  });
});
