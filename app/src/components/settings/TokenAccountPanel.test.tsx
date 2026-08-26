import { fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: vi.fn() },
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));

const api = vi.hoisted(() => ({
  getGoogleAccounts: vi.fn(),
  getDockerAccounts: vi.fn(),
  getDockerAccountToken: vi.fn(),
  saveDockerAccount: vi.fn(),
  deleteDockerAccount: vi.fn(),
  getDockerAccountUsage: vi.fn(),
  getDockerAccountTOTP: vi.fn(),
  bindDockerAccount: vi.fn(),
  getAliyunAccounts: vi.fn(),
  getAliyunAccountSecret: vi.fn(),
  saveAliyunAccount: vi.fn(),
  deleteAliyunAccount: vi.fn(),
  getAliyunAccountUsage: vi.fn(),
  getAliyunAccountTOTP: vi.fn(),
  bindAliyunAccount: vi.fn(),
  inspectDockerAccount: vi.fn(),
  inspectAliyunAccount: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));
vi.mock('../../services/agentSend', () => ({ sendToAgent: vi.fn() }));
vi.mock('./GoogleAccountsPanel', () => ({ openChromeProfile: vi.fn() }));

import DockerAccountsPanel from './DockerAccountsPanel';
import AliyunAccountsPanel from './AliyunAccountsPanel';

const dockerAccount = {
  name: 'cicy-ai',
  username: 'cicyrobot',
  email: 'ops@cicy.de5.net',
  token_set: true,
  token_tail: 'GPTK',
  '2fa_set': false,
  profile: '',
  password_set: false,
  registry: 'docker.io',
};
const aliyunAccount = {
  name: 'cicy-prod',
  access_key_id: 'LTAI5tExample',
  secret_set: true,
  secret_tail: 'x9QZ',
  region: 'cn-hangzhou',
  account: 'ops@cicy.de5.net',
  email: 'ops@cicy.de5.net',
  '2fa_set': false,
  profile: '',
  password_set: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  api.getGoogleAccounts.mockResolvedValue({ data: { accounts: [{ profile: 'work' }] } });
  api.getDockerAccounts.mockResolvedValue({ data: { accounts: [dockerAccount] } });
  api.getDockerAccountUsage.mockResolvedValue({
    data: { username: 'cicyrobot', registry: 'docker.io', repositories: 4, private_repos: 1, pulls: 2400000, pull_limit: 200, pull_remain: 176 },
  });
  api.getDockerAccountToken.mockResolvedValue({ data: { api_token: 'dckr_pat_x', '2fa': '', password: '' } });
  api.bindDockerAccount.mockResolvedValue({ data: { success: true, registry: 'docker.io' } });
  api.getAliyunAccounts.mockResolvedValue({ data: { accounts: [aliyunAccount] } });
  api.getAliyunAccountSecret.mockResolvedValue({ data: { access_key_id: 'LTAI5tExample', access_key_secret: 'super-secret', '2fa': '', password: '' } });
  api.getAliyunAccountUsage.mockResolvedValue({
    data: { account_id: '1799', user_id: '2001', identity_type: 'RAMUser', balance: '128.50', currency: 'CNY', balance_available: true },
  });
  api.bindAliyunAccount.mockResolvedValue({ data: { success: true } });
  api.inspectAliyunAccount.mockResolvedValue({
    data: { account_id: '1799', user_id: '2001', arn: 'acs:ram::1799:user/cicy-dev', identity_type: 'RAMUser', user_name: 'cicy-dev', display_name: '开发子账号', email: 'dev@cicy.de5.net', region: 'cn-hangzhou', balance: '128.50', currency: 'CNY', notes: [] },
  });
  api.inspectDockerAccount.mockResolvedValue({
    data: { username: 'cicyrobot', full_name: 'CiCy Robot', email: 'ops@cicy.de5.net', registry: 'docker.io', orgs: ['cicyai'], repositories: 4, pulls: 10, pull_limit: 200, pull_remain: 176, notes: [] },
  });
});

describe('<DockerAccountsPanel />', () => {
  it('shows the login name, masked token and Hub pull budget', async () => {
    render(<DockerAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-cicy-ai"]')).toBeTruthy());
    expect(document.querySelector('[data-id="docker-account-meta"]')).toHaveTextContent('cicyrobot');
    expect(document.querySelector('[data-id="docker-account-meta"]')).toHaveTextContent('Token ••••GPTK');
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-usage"]')).toHaveTextContent('4 个仓库'));
    expect(document.querySelector('[data-id="docker-account-usage"]')).toHaveTextContent('拉取 2.4M');
    expect(document.querySelector('[data-id="docker-account-usage"]')).toHaveTextContent('限额 176/200');
    // docker.io is the default, so it stays out of the name row.
    expect(document.querySelector('[data-id="docker-account-registry"]')).toBeNull();
  });

  it('binds the credential into ~/.docker/config.json', async () => {
    render(<DockerAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-more"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="docker-account-more"]')!);
    fireEvent.click(document.querySelector('[data-id="docker-account-bind"]')!);
    await waitFor(() => expect(api.bindDockerAccount).toHaveBeenCalledWith('cicy-ai'));
  });
});

describe('shared panel shell', () => {
  // The platform panels rebuild their `api` prop on every render, so a loader
  // keyed on its identity would re-fetch forever.
  it('loads the account list once, not on every render', async () => {
    render(<DockerAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-cicy-ai"]')).toBeTruthy());
    await waitFor(() => expect(api.getDockerAccountUsage).toHaveBeenCalledTimes(1));
    expect(api.getDockerAccounts).toHaveBeenCalledTimes(1);
  });

  // The Chrome profile is not one of the platform fields, so it has to be
  // carried into the edit form explicitly or a save would clear it.
  it('keeps the Chrome profile when editing', async () => {
    api.getDockerAccounts.mockResolvedValue({ data: { accounts: [{ ...dockerAccount, profile: 'work' }] } });
    render(<DockerAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-more"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="docker-account-more"]')!);
    fireEvent.click(document.querySelector('[data-id="docker-account-edit"]')!);
    await waitFor(() => expect((document.querySelector('[data-id="docker-account-profile-select"]') as HTMLSelectElement)?.value).toBe('work'));
    fireEvent.click(document.querySelector('[data-id="docker-account-save"]')!);
    await waitFor(() => expect(api.saveDockerAccount).toHaveBeenCalled());
    expect(api.saveDockerAccount.mock.calls[0][0].profile).toBe('work');
  });
});

describe('credential auto-fill', () => {
  // Docker Hub needs the login name to exchange the PAT, so the button stays
  // disabled until both halves are present.
  it('fills the Docker account from a PAT plus login name', async () => {
    render(<DockerAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="docker-account-add"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="docker-account-add"]')!);
    const inspect = document.querySelector('[data-id="docker-account-inspect"]') as HTMLButtonElement;
    fireEvent.change(document.querySelector('[data-id="docker-account-token-input"]')!, { target: { value: 'dckr_pat_x' } });
    expect(inspect.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="docker-account-name-input"]')!, { target: { value: 'cicyrobot' } });
    expect(inspect.disabled).toBe(false);
    fireEvent.click(inspect);
    await waitFor(() => expect((document.querySelector('[data-id="docker-account-username-input"]') as HTMLInputElement).value).toBe('cicyrobot'));
    expect((document.querySelector('[data-id="docker-account-email-input"]') as HTMLInputElement).value).toBe('ops@cicy.de5.net');
    expect(document.querySelector('[data-id="docker-account-inspect-summary"]')).toHaveTextContent('CiCy Robot');
    expect(document.querySelector('[data-id="docker-account-inspect-summary"]')).toHaveTextContent('限额 176/200');
  });

  it('fills the Aliyun account from an AccessKey pair', async () => {
    render(<AliyunAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="aliyun-account-add"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="aliyun-account-add"]')!);
    const inspect = document.querySelector('[data-id="aliyun-account-inspect"]') as HTMLButtonElement;
    fireEvent.change(document.querySelector('[data-id="aliyun-account-ak-input"]')!, { target: { value: 'LTAI-dev' } });
    expect(inspect.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="aliyun-account-secret-input"]')!, { target: { value: 'dev-secret' } });
    expect(inspect.disabled).toBe(false);
    fireEvent.click(inspect);
    await waitFor(() => expect((document.querySelector('[data-id="aliyun-account-name-input"]') as HTMLInputElement).value).toBe('cicy-dev'));
    expect((document.querySelector('[data-id="aliyun-account-account-input"]') as HTMLInputElement).value).toBe('开发子账号');
    expect((document.querySelector('[data-id="aliyun-account-region-input"]') as HTMLInputElement).value).toBe('cn-hangzhou');
    expect(document.querySelector('[data-id="aliyun-account-inspect-summary"]')).toHaveTextContent('RAMUser');
    expect(document.querySelector('[data-id="aliyun-account-inspect-summary"]')).toHaveTextContent('余额 128.50 CNY');
  });
});

describe('<AliyunAccountsPanel />', () => {
  it('shows the AccessKey ID but only the tail of the secret', async () => {
    render(<AliyunAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="aliyun-account-cicy-prod"]')).toBeTruthy());
    expect(document.querySelector('[data-id="aliyun-account-meta"]')).toHaveTextContent('LTAI5tExample');
    expect(document.querySelector('[data-id="aliyun-account-meta"]')).toHaveTextContent('Secret ••••x9QZ');
    expect(document.querySelector('[data-id="aliyun-account-region"]')).toHaveTextContent('cn-hangzhou');
    await waitFor(() => expect(document.querySelector('[data-id="aliyun-account-usage"]')).toHaveTextContent('余额 128.50 CNY'));
  });

  it('requires both AccessKey ID and secret when creating', async () => {
    render(<AliyunAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="aliyun-account-add"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="aliyun-account-add"]')!);
    const save = document.querySelector('[data-id="aliyun-account-save"]') as HTMLButtonElement;
    fireEvent.change(document.querySelector('[data-id="aliyun-account-name-input"]')!, { target: { value: 'cicy-dev' } });
    expect(save.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="aliyun-account-ak-input"]')!, { target: { value: 'LTAI-dev' } });
    expect(save.disabled).toBe(true);
    fireEvent.change(document.querySelector('[data-id="aliyun-account-secret-input"]')!, { target: { value: 'dev-secret' } });
    expect(save.disabled).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(api.saveAliyunAccount).toHaveBeenCalledWith(expect.objectContaining({ name: 'cicy-dev', access_key_id: 'LTAI-dev', access_key_secret: 'dev-secret' })));
  });

  // Clearing a secret field on edit means "keep what is stored", so it must be
  // omitted from the request rather than sent as an empty string.
  it('omits a blank secret when editing instead of wiping it', async () => {
    render(<AliyunAccountsPanel active paneId="w-101:main.0" />);
    await waitFor(() => expect(document.querySelector('[data-id="aliyun-account-more"]')).toBeTruthy());
    fireEvent.click(document.querySelector('[data-id="aliyun-account-more"]')!);
    fireEvent.click(document.querySelector('[data-id="aliyun-account-edit"]')!);
    await waitFor(() => expect((document.querySelector('[data-id="aliyun-account-secret-input"]') as HTMLInputElement)?.value).toBe('super-secret'));
    fireEvent.change(document.querySelector('[data-id="aliyun-account-secret-input"]')!, { target: { value: '' } });
    fireEvent.click(document.querySelector('[data-id="aliyun-account-save"]')!);
    await waitFor(() => expect(api.saveAliyunAccount).toHaveBeenCalled());
    const body = api.saveAliyunAccount.mock.calls[0][0];
    expect(body.access_key_secret).toBeUndefined();
    expect(body.old_name).toBe('cicy-prod');
    expect(body.access_key_id).toBe('LTAI5tExample');
  });
});
