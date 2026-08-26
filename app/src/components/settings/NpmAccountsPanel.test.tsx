import { fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  getNpmAccounts: vi.fn(),
  getGoogleAccounts: vi.fn(),
  getNpmAccountUsage: vi.fn(),
  getNpmAccountToken: vi.fn(),
  saveNpmAccount: vi.fn(),
  inspectNpmAccount: vi.fn(),
  deleteNpmAccount: vi.fn(),
  bindNpmAccount: vi.fn(),
  getNpmAccountTOTP: vi.fn(),
}));
const agentSend = vi.hoisted(() => ({ sendToAgent: vi.fn() }));
vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => agentSend);
vi.mock('./GoogleAccountsPanel', () => ({ openChromeProfile: vi.fn() }));

import NpmAccountsPanel from './NpmAccountsPanel';

const account = {
  name: 'cicy-ai',
  email: 'npm@cicy.de5.net',
  token_set: true,
  token_tail: 'm9EI',
  '2fa_set': false,
  profile: '',
  password_set: false,
  registry: 'https://registry.npmjs.org',
  scope: '@cicy',
};

beforeEach(() => {
  vi.clearAllMocks();
  api.getNpmAccounts.mockResolvedValue({ data: { accounts: [account] } });
  api.getGoogleAccounts.mockResolvedValue({ data: { accounts: [] } });
  api.getNpmAccountUsage.mockResolvedValue({
    data: { username: 'cicy-ai', registry: 'https://registry.npmjs.org', packages: 3, last_publish: '2026-08-20T10:00:00.000Z', downloads: 12500, downloads_partial: false, packages_truncated: false },
  });
  api.bindNpmAccount.mockResolvedValue({ data: { success: true, registry: 'https://registry.npmjs.org' } });
  api.inspectNpmAccount.mockResolvedValue({
    data: {
      username: 'cicy-ai',
      email: 'npm@cicy.de5.net',
      registry: 'https://registry.npmjs.org',
      tfa_mode: 'auth-and-writes',
      scopes: ['@cicy'],
      packages: 3,
      public_packages: 2,
      private_packages: 1,
      token_automation: true,
      notes: [],
    },
  });
});

describe('<NpmAccountsPanel />', () => {
  // The token must never render in full — only the stored last four characters.
  it('lists accounts with a masked token tail and registry usage', async () => {
    render(<NpmAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="npm-account-cicy-ai"]')).toBeTruthy());
    expect(document.querySelector('[data-id="npm-account-meta"]')).toHaveTextContent('Token ••••m9EI');
    expect(document.querySelector('[data-id="npm-account-scope"]')).toHaveTextContent('@cicy');
    await waitFor(() => expect(document.querySelector('[data-id="npm-account-usage"]')).toHaveTextContent('3 个包'));
    expect(document.querySelector('[data-id="npm-account-usage"]')).toHaveTextContent('月下载 12.5k');
    expect(document.querySelector('[data-id="npm-account-usage"]')).toHaveTextContent('2026-08-20');
  });

  it('binds the account to ~/.npmrc from the card menu', async () => {
    render(<NpmAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="npm-account-more"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="npm-account-more"]')!);
    fireEvent.click(document.querySelector('[data-id="npm-account-bind"]')!);
    await waitFor(() => expect(api.bindNpmAccount).toHaveBeenCalledWith('cicy-ai'));
  });

  // The whole point of the auto-fill: paste one token, get the identity back.
  it('fills name, email, registry and scope from a pasted token', async () => {
    render(<NpmAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="npm-account-add"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="npm-account-add"]')!);
    const inspect = document.querySelector('[data-id="npm-account-inspect"]') as HTMLButtonElement;
    // Nothing to probe with until a token is pasted.
    expect(inspect.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="npm-account-token-input"]')!, { target: { value: 'npm_secret' } });
    expect(inspect.disabled).toBe(false);
    fireEvent.click(inspect);
    await waitFor(() => expect((document.querySelector('[data-id="npm-account-name-input"]') as HTMLInputElement).value).toBe('cicy-ai'));
    expect((document.querySelector('[data-id="npm-account-email-input"]') as HTMLInputElement).value).toBe('npm@cicy.de5.net');
    expect((document.querySelector('[data-id="npm-account-registry-input"]') as HTMLInputElement).value).toBe('https://registry.npmjs.org');
    expect((document.querySelector('[data-id="npm-account-scope-input"]') as HTMLInputElement).value).toBe('@cicy');
    expect(api.inspectNpmAccount).toHaveBeenCalledWith({ api_token: 'npm_secret', registry: '' });
    const summary = document.querySelector('[data-id="npm-account-inspect-summary"]');
    expect(summary).toHaveTextContent('2FA auth-and-writes');
    expect(summary).toHaveTextContent('3 个包');
    expect(summary).toHaveTextContent('1 个私有');
    // Saving right after the auto-fill must carry the resolved identity.
    fireEvent.click(document.querySelector('[data-id="npm-account-save"]')!);
    await waitFor(() => expect(api.saveNpmAccount).toHaveBeenCalledWith(expect.objectContaining({ name: 'cicy-ai', email: 'npm@cicy.de5.net', scope: '@cicy', api_token: 'npm_secret' })));
  });

  // A new account without a token is not savable; an edit may leave it blank.
  it('requires a token when creating an account', async () => {
    render(<NpmAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="npm-account-add"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="npm-account-add"]')!);
    const save = document.querySelector('[data-id="npm-account-save"]') as HTMLButtonElement;
    fireEvent.change(document.querySelector('[data-id="npm-account-name-input"]')!, { target: { value: 'cicy-dev' } });
    expect(save.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="npm-account-token-input"]')!, { target: { value: 'npm_secret' } });
    expect(save.disabled).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(api.saveNpmAccount).toHaveBeenCalledWith(expect.objectContaining({ name: 'cicy-dev', api_token: 'npm_secret' })));
  });
});
