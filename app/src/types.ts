export interface Position {
  x: number;
  y: number;
}

export interface EditPaneData {
  target: string;
  title: string;
  agent_duty?: string;
  agent_type?: string;
  allow_all_actions?: boolean;
  use_custom_gateway?: boolean;
  use_proxy?: boolean;
  proxy?: {
    password?: string;
    rule?: string;
  } | null;
  workspace?: string;
  active?: boolean;
  init_script?: string;
  tg_enable?: boolean;
  tg_token?: string;
  tg_chat_id?: string;
  url?: string;
  config?: string;
  ttyd_preview?: string;
  role?: string;
  default_model?: string;
  runtime_ai?: {
    provider_name?: string;
    provider_protocol?: string;
    model?: string;
  } | null;
}
