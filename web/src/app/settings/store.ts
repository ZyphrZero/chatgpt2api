"use client";

import { create } from "zustand";
import { toast } from "sonner";

import {
  cleanupLogs,
  createCPAPool,
  deleteCPAPool,
  fetchCPAPoolFiles,
  fetchCPAPools,
  fetchLogGovernance,
  fetchRegisterConfig,
  resetRegister as resetRegisterApi,
  fetchSettingsConfig,
  startRegister,
  startCPAImport,
  stopRegister,
  updateCPAPool,
  updateLoginPageImageSettings,
  updateRegisterConfig,
  updateSettingsConfig,
  type CPAPool,
  type CPARemoteFile,
  type LogCleanupResult,
  type LogGovernanceSummary,
  type LoginPageImageSettings,
  type RegisterConfig,
  type SettingsConfig,
} from "@/lib/api";
import { dispatchAppMetaUpdated } from "@/lib/app-meta";
import {
  LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM,
  normalizeLoginPageImageMode,
  normalizeLoginPageImageTransform,
  type LoginPageImageMode,
} from "@/lib/login-page-image-layout";

export const PAGE_SIZE_OPTIONS = ["50", "100", "200"] as const;

export type PageSizeOption = (typeof PAGE_SIZE_OPTIONS)[number];

function normalizeConfig(config: SettingsConfig): SettingsConfig {
  const loginImageTransform = normalizeLoginPageImageTransform({
    zoom: Number(config.login_page_image_zoom),
    positionX: Number(config.login_page_image_position_x),
    positionY: Number(config.login_page_image_position_y),
  });
  return {
    ...config,
    refresh_account_interval_minute: Number(config.refresh_account_interval_minute || 5),
    image_concurrent_limit: Number(config.image_concurrent_limit || 8),
    image_task_timeout_seconds: Number(config.image_task_timeout_seconds || 300),
    user_default_concurrent_limit: Number(config.user_default_concurrent_limit || 0),
    user_default_rpm_limit: Number(config.user_default_rpm_limit || 0),
    image_retention_days: Number(config.image_retention_days || 30),
    log_retention_days: Number(config.log_retention_days || 7),
    auto_remove_invalid_accounts: Boolean(config.auto_remove_invalid_accounts),
    auto_remove_rate_limited_accounts: Boolean(config.auto_remove_rate_limited_accounts),
    log_levels: Array.isArray(config.log_levels) ? config.log_levels : [],
    proxy: typeof config.proxy === "string" ? config.proxy : "",
    base_url: typeof config.base_url === "string" ? config.base_url : "",
    registration_enabled: Boolean(config.registration_enabled),
    user_free_quota: Math.max(0, Number(config.user_free_quota ?? 10) || 0),
    invite_reward_quota: Math.max(0, Number(config.invite_reward_quota ?? 0) || 0),
    invitee_bonus_quota: Math.max(0, Number(config.invitee_bonus_quota ?? 0) || 0),
    turnstile_enabled: Boolean(config.turnstile_enabled),
    turnstile_site_key: typeof config.turnstile_site_key === "string" ? config.turnstile_site_key : "",
    turnstile_secret_key: "",
    turnstile_secret_configured: Boolean(config.turnstile_secret_configured),
    linuxdo_enabled: Boolean(config.linuxdo_enabled),
    linuxdo_client_id: typeof config.linuxdo_client_id === "string" ? config.linuxdo_client_id : "",
    linuxdo_client_secret: "",
    linuxdo_client_secret_configured: Boolean(config.linuxdo_client_secret_configured),
    linuxdo_redirect_url: typeof config.linuxdo_redirect_url === "string" ? config.linuxdo_redirect_url : "",
    linuxdo_frontend_redirect_url:
      typeof config.linuxdo_frontend_redirect_url === "string" ? config.linuxdo_frontend_redirect_url : "/auth/linuxdo/callback",
    qq_enabled: Boolean(config.qq_enabled),
    qq_client_id: typeof config.qq_client_id === "string" ? config.qq_client_id : "",
    qq_client_secret: "",
    qq_client_secret_configured: Boolean(config.qq_client_secret_configured),
    qq_redirect_url: typeof config.qq_redirect_url === "string" ? config.qq_redirect_url : "",
    qq_frontend_redirect_url:
      typeof config.qq_frontend_redirect_url === "string" ? config.qq_frontend_redirect_url : "/auth/qq/callback",
    social_login_base_url: typeof config.social_login_base_url === "string" ? config.social_login_base_url : "",
    social_login_app_id: typeof config.social_login_app_id === "string" ? config.social_login_app_id : "",
    social_login_app_key: "",
    social_login_app_key_configured: Boolean(config.social_login_app_key_configured),
    social_login_redirect_url:
      typeof config.social_login_redirect_url === "string" ? config.social_login_redirect_url : "",
    social_login_frontend_redirect_url:
      typeof config.social_login_frontend_redirect_url === "string" ? config.social_login_frontend_redirect_url : "/auth/social/callback",
    social_login_qq_enabled: Boolean(config.social_login_qq_enabled),
    social_login_wx_enabled: Boolean(config.social_login_wx_enabled),
    social_login_douyin_enabled: Boolean(config.social_login_douyin_enabled),
    update_repo: typeof config.update_repo === "string" ? config.update_repo : "ZyphrZero/chatgpt2api",
    update_github_token: "",
    update_github_token_configured: Boolean(config.update_github_token_configured),
    login_page_image_url: typeof config.login_page_image_url === "string" ? config.login_page_image_url : "",
    login_page_image_mode: normalizeLoginPageImageMode(config.login_page_image_mode),
    login_page_image_zoom: loginImageTransform.zoom,
    login_page_image_position_x: loginImageTransform.positionX,
    login_page_image_position_y: loginImageTransform.positionY,
  };
}

function normalizeFiles(items: CPARemoteFile[]) {
  const seen = new Set<string>();
  const files: CPARemoteFile[] = [];
  for (const item of items) {
    const name = String(item.name || "").trim();
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    files.push({
      name,
      email: String(item.email || "").trim(),
    });
  }
  return files;
}

type SettingsStore = {
  config: SettingsConfig | null;
  isLoadingConfig: boolean;
  isSavingConfig: boolean;
  logGovernance: LogGovernanceSummary | null;
  lastLogCleanup: LogCleanupResult | null;
  isLoadingLogGovernance: boolean;
  isCleaningLogs: boolean;

  registerConfig: RegisterConfig | null;
  isLoadingRegister: boolean;
  isSavingRegister: boolean;

  pools: CPAPool[];
  isLoadingPools: boolean;
  deletingId: string | null;
  loadingFilesId: string | null;

  dialogOpen: boolean;
  editingPool: CPAPool | null;
  formName: string;
  formBaseUrl: string;
  formSecretKey: string;
  showSecret: boolean;
  isSavingPool: boolean;

  browserOpen: boolean;
  browserPool: CPAPool | null;
  remoteFiles: CPARemoteFile[];
  selectedNames: string[];
  fileQuery: string;
  filePage: number;
  pageSize: PageSizeOption;
  isStartingImport: boolean;

  initialize: () => Promise<void>;
  loadConfig: () => Promise<void>;
  saveConfig: () => Promise<void>;
  setRefreshAccountIntervalMinute: (value: string) => void;
  setImageConcurrentLimit: (value: string) => void;
  setImageTaskTimeoutSeconds: (value: string) => void;
  setUserDefaultConcurrentLimit: (value: string) => void;
  setUserDefaultRpmLimit: (value: string) => void;
  setImageRetentionDays: (value: string) => void;
  setLogRetentionDays: (value: string) => void;
  setAutoRemoveInvalidAccounts: (value: boolean) => void;
  setAutoRemoveRateLimitedAccounts: (value: boolean) => void;
  setLogLevel: (level: string, enabled: boolean) => void;
  setProxy: (value: string) => void;
  setBaseUrl: (value: string) => void;
  setRegistrationEnabled: (value: boolean) => void;
  setUserFreeQuota: (value: string) => void;
  setInviteRewardQuota: (value: string) => void;
  setInviteeBonusQuota: (value: string) => void;
  setTurnstileEnabled: (value: boolean) => void;
  setTurnstileSiteKey: (value: string) => void;
  setTurnstileSecretKey: (value: string) => void;
  setLinuxDoEnabled: (value: boolean) => void;
  setLinuxDoClientId: (value: string) => void;
  setLinuxDoClientSecret: (value: string) => void;
  setLinuxDoRedirectUrl: (value: string) => void;
  setLinuxDoFrontendRedirectUrl: (value: string) => void;
  setQQEnabled: (value: boolean) => void;
  setQQClientId: (value: string) => void;
  setQQClientSecret: (value: string) => void;
  setQQRedirectUrl: (value: string) => void;
  setQQFrontendRedirectUrl: (value: string) => void;
  setSocialLoginBaseUrl: (value: string) => void;
  setSocialLoginAppId: (value: string) => void;
  setSocialLoginAppKey: (value: string) => void;
  setSocialLoginRedirectUrl: (value: string) => void;
  setSocialLoginFrontendRedirectUrl: (value: string) => void;
  setSocialLoginQQEnabled: (value: boolean) => void;
  setSocialLoginWXEnabled: (value: boolean) => void;
  setSocialLoginDouyinEnabled: (value: boolean) => void;
  setUpdateRepo: (value: string) => void;
  setUpdateGitHubToken: (value: string) => void;
  setLoginPageImageUrl: (value: string) => void;
  setLoginPageImageMode: (value: LoginPageImageMode) => void;
  setLoginPageImageTransform: (transform: { zoom: number; positionX: number; positionY: number }) => void;
  restoreDefaultLoginPageImage: () => void;
  saveLoginPageImage: (options: { file?: File | null; action: "keep" | "replace" | "remove" }) => Promise<boolean>;
  loadLogGovernance: (silent?: boolean) => Promise<void>;
  cleanupLogsByRetention: () => Promise<void>;

  loadRegister: (silent?: boolean) => Promise<void>;
  setRegisterConfig: (config: RegisterConfig) => void;
  setRegisterProxy: (value: string) => void;
  setRegisterTotal: (value: string) => void;
  setRegisterThreads: (value: string) => void;
  setRegisterMode: (value: "total" | "quota" | "available") => void;
  setRegisterTargetQuota: (value: string) => void;
  setRegisterTargetAvailable: (value: string) => void;
  setRegisterCheckInterval: (value: string) => void;
  setRegisterMailField: (key: "request_timeout" | "wait_timeout" | "wait_interval", value: string) => void;
  addRegisterProvider: () => void;
  updateRegisterProvider: (index: number, updates: Record<string, unknown>) => void;
  deleteRegisterProvider: (index: number) => void;
  saveRegister: () => Promise<void>;
  toggleRegister: () => Promise<void>;
  resetRegister: () => Promise<void>;

  loadPools: (silent?: boolean) => Promise<void>;
  openAddDialog: () => void;
  openEditDialog: (pool: CPAPool) => void;
  setDialogOpen: (open: boolean) => void;
  setFormName: (value: string) => void;
  setFormBaseUrl: (value: string) => void;
  setFormSecretKey: (value: string) => void;
  setShowSecret: (checked: boolean) => void;
  savePool: () => Promise<void>;
  deletePool: (pool: CPAPool) => Promise<void>;

  browseFiles: (pool: CPAPool) => Promise<void>;
  setBrowserOpen: (open: boolean) => void;
  toggleFile: (name: string, checked: boolean) => void;
  replaceSelectedNames: (names: string[]) => void;
  setFileQuery: (value: string) => void;
  setFilePage: (page: number) => void;
  setPageSize: (value: PageSizeOption) => void;
  startImport: () => Promise<void>;
};

export const useSettingsStore = create<SettingsStore>((set, get) => ({
  config: null,
  isLoadingConfig: true,
  isSavingConfig: false,
  logGovernance: null,
  lastLogCleanup: null,
  isLoadingLogGovernance: true,
  isCleaningLogs: false,

  registerConfig: null,
  isLoadingRegister: true,
  isSavingRegister: false,

  pools: [],
  isLoadingPools: true,
  deletingId: null,
  loadingFilesId: null,

  dialogOpen: false,
  editingPool: null,
  formName: "",
  formBaseUrl: "",
  formSecretKey: "",
  showSecret: false,
  isSavingPool: false,

  browserOpen: false,
  browserPool: null,
  remoteFiles: [],
  selectedNames: [],
  fileQuery: "",
  filePage: 1,
  pageSize: "100",
  isStartingImport: false,

  initialize: async () => {
    await Promise.allSettled([get().loadConfig(), get().loadPools(), get().loadLogGovernance()]);
  },

  loadConfig: async () => {
    set({ isLoadingConfig: true });
    try {
      const data = await fetchSettingsConfig();
      set({
        config: normalizeConfig(data.config),
      });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载系统配置失败");
    } finally {
      set({ isLoadingConfig: false });
    }
  },

  saveConfig: async () => {
    const { config } = get();
    if (!config) {
      return;
    }

    set({ isSavingConfig: true });
    try {
      const linuxDoClientSecret = String(config.linuxdo_client_secret || "").trim();
      const qqClientSecret = String(config.qq_client_secret || "").trim();
      const socialLoginAppKey = String(config.social_login_app_key || "").trim();
      const updateGitHubToken = String(config.update_github_token || "").trim();
      const turnstileSecretKey = String(config.turnstile_secret_key || "").trim();
      const payload: SettingsConfig = {
        ...config,
        refresh_account_interval_minute: Math.max(1, Number(config.refresh_account_interval_minute) || 1),
        image_concurrent_limit: Math.max(1, Number(config.image_concurrent_limit) || 8),
        image_task_timeout_seconds: Math.min(3600, Math.max(30, Number(config.image_task_timeout_seconds) || 300)),
        user_default_concurrent_limit: Math.max(0, Number(config.user_default_concurrent_limit) || 0),
        user_default_rpm_limit: Math.max(0, Number(config.user_default_rpm_limit) || 0),
        image_retention_days: Math.max(1, Number(config.image_retention_days) || 30),
        log_retention_days: Math.min(3650, Math.max(1, Number(config.log_retention_days) || 7)),
        auto_remove_invalid_accounts: Boolean(config.auto_remove_invalid_accounts),
        auto_remove_rate_limited_accounts: Boolean(config.auto_remove_rate_limited_accounts),
        proxy: config.proxy.trim(),
        base_url: String(config.base_url || "").trim(),
        registration_enabled: Boolean(config.registration_enabled),
        user_free_quota: Math.max(0, Number(config.user_free_quota) || 0),
        invite_reward_quota: Math.max(0, Number(config.invite_reward_quota) || 0),
        invitee_bonus_quota: Math.max(0, Number(config.invitee_bonus_quota) || 0),
        turnstile_enabled: Boolean(config.turnstile_enabled),
        turnstile_site_key: String(config.turnstile_site_key || "").trim(),
        turnstile_secret_key: turnstileSecretKey,
        linuxdo_enabled: Boolean(config.linuxdo_enabled),
        linuxdo_client_id: String(config.linuxdo_client_id || "").trim(),
        linuxdo_client_secret: linuxDoClientSecret,
        linuxdo_redirect_url: String(config.linuxdo_redirect_url || "").trim(),
        linuxdo_frontend_redirect_url: String(config.linuxdo_frontend_redirect_url || "").trim(),
        qq_enabled: Boolean(config.qq_enabled),
        qq_client_id: String(config.qq_client_id || "").trim(),
        qq_client_secret: qqClientSecret,
        qq_redirect_url: String(config.qq_redirect_url || "").trim(),
        qq_frontend_redirect_url: String(config.qq_frontend_redirect_url || "").trim(),
        social_login_base_url: String(config.social_login_base_url || "").trim(),
        social_login_app_id: String(config.social_login_app_id || "").trim(),
        social_login_app_key: socialLoginAppKey,
        social_login_redirect_url: String(config.social_login_redirect_url || "").trim(),
        social_login_frontend_redirect_url: String(config.social_login_frontend_redirect_url || "").trim(),
        social_login_qq_enabled: Boolean(config.social_login_qq_enabled),
        social_login_wx_enabled: Boolean(config.social_login_wx_enabled),
        social_login_douyin_enabled: Boolean(config.social_login_douyin_enabled),
        update_repo: String(config.update_repo ?? "ZyphrZero/chatgpt2api").trim(),
        update_github_token: updateGitHubToken,
      };
      if (!linuxDoClientSecret) {
        delete payload.linuxdo_client_secret;
      }
      if (!qqClientSecret) {
        delete payload.qq_client_secret;
      }
      if (!socialLoginAppKey) {
        delete payload.social_login_app_key;
      }
      if (!updateGitHubToken) {
        delete payload.update_github_token;
      }
      if (!turnstileSecretKey) {
        delete payload.turnstile_secret_key;
      }
      delete payload.linuxdo_client_secret_configured;
      delete payload.qq_client_secret_configured;
      delete payload.social_login_app_key_configured;
      delete payload.update_github_token_configured;
      delete payload.turnstile_secret_configured;

      const data = await updateSettingsConfig(payload);
      set({
        config: normalizeConfig(data.config),
      });
      toast.success("配置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存系统配置失败");
    } finally {
      set({ isSavingConfig: false });
    }
  },

  setRefreshAccountIntervalMinute: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          refresh_account_interval_minute: value,
        },
      };
    });
  },

  setImageRetentionDays: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_retention_days: value } } : {});
  },

  setLogRetentionDays: (value) => {
    set((state) => state.config ? { config: { ...state.config, log_retention_days: value } } : {});
  },

  setImageConcurrentLimit: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_concurrent_limit: value } } : {});
  },

  setImageTaskTimeoutSeconds: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_task_timeout_seconds: value } } : {});
  },

  setUserDefaultConcurrentLimit: (value) => {
    set((state) => state.config ? { config: { ...state.config, user_default_concurrent_limit: value } } : {});
  },

  setUserDefaultRpmLimit: (value) => {
    set((state) => state.config ? { config: { ...state.config, user_default_rpm_limit: value } } : {});
  },

  setAutoRemoveInvalidAccounts: (value) => {
    set((state) => state.config ? { config: { ...state.config, auto_remove_invalid_accounts: value } } : {});
  },

  setAutoRemoveRateLimitedAccounts: (value) => {
    set((state) => state.config ? { config: { ...state.config, auto_remove_rate_limited_accounts: value } } : {});
  },

  setLogLevel: (level, enabled) => {
    set((state) => {
      if (!state.config) return {};
      const levels = new Set(state.config.log_levels || []);
      if (enabled) levels.add(level);
      else levels.delete(level);
      return { config: { ...state.config, log_levels: Array.from(levels) } };
    });
  },

  setProxy: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          proxy: value,
        },
      };
    });
  },

  setBaseUrl: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          base_url: value,
        },
      };
    });
  },

  setRegistrationEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, registration_enabled: value } } : {});
  },

  setUserFreeQuota: (value) => {
    set((state) => state.config ? { config: { ...state.config, user_free_quota: value } } : {});
  },

  setInviteRewardQuota: (value) => {
    set((state) => state.config ? { config: { ...state.config, invite_reward_quota: value } } : {});
  },

  setInviteeBonusQuota: (value) => {
    set((state) => state.config ? { config: { ...state.config, invitee_bonus_quota: value } } : {});
  },

  setTurnstileEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, turnstile_enabled: value } } : {});
  },

  setTurnstileSiteKey: (value) => {
    set((state) => state.config ? { config: { ...state.config, turnstile_site_key: value } } : {});
  },

  setTurnstileSecretKey: (value) => {
    set((state) => state.config ? { config: { ...state.config, turnstile_secret_key: value } } : {});
  },

  setLinuxDoEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_enabled: value } } : {});
  },

  setLinuxDoClientId: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_client_id: value } } : {});
  },

  setLinuxDoClientSecret: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_client_secret: value } } : {});
  },

  setLinuxDoRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_redirect_url: value } } : {});
  },

  setLinuxDoFrontendRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_frontend_redirect_url: value } } : {});
  },

  setQQEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, qq_enabled: value } } : {});
  },

  setQQClientId: (value) => {
    set((state) => state.config ? { config: { ...state.config, qq_client_id: value } } : {});
  },

  setQQClientSecret: (value) => {
    set((state) => state.config ? { config: { ...state.config, qq_client_secret: value } } : {});
  },

  setQQRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, qq_redirect_url: value } } : {});
  },

  setQQFrontendRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, qq_frontend_redirect_url: value } } : {});
  },

  setSocialLoginBaseUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_base_url: value } } : {});
  },

  setSocialLoginAppId: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_app_id: value } } : {});
  },

  setSocialLoginAppKey: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_app_key: value } } : {});
  },

  setSocialLoginRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_redirect_url: value } } : {});
  },

  setSocialLoginFrontendRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_frontend_redirect_url: value } } : {});
  },

  setSocialLoginQQEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_qq_enabled: value } } : {});
  },

  setSocialLoginWXEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_wx_enabled: value } } : {});
  },

  setSocialLoginDouyinEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, social_login_douyin_enabled: value } } : {});
  },

  setUpdateRepo: (value) => {
    set((state) => state.config ? { config: { ...state.config, update_repo: value } } : {});
  },

  setUpdateGitHubToken: (value) => {
    set((state) => state.config ? { config: { ...state.config, update_github_token: value } } : {});
  },

  setLoginPageImageUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, login_page_image_url: value } } : {});
  },

  setLoginPageImageMode: (value) => {
    set((state) => state.config ? { config: { ...state.config, login_page_image_mode: value } } : {});
  },

  setLoginPageImageTransform: (transform) => {
    const normalized = normalizeLoginPageImageTransform(transform);
    set((state) => state.config ? {
      config: {
        ...state.config,
        login_page_image_zoom: normalized.zoom,
        login_page_image_position_x: normalized.positionX,
        login_page_image_position_y: normalized.positionY,
      },
    } : {});
  },

  restoreDefaultLoginPageImage: () => {
    set((state) => state.config ? {
      config: {
        ...state.config,
        login_page_image_url: "",
        login_page_image_zoom: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
        login_page_image_position_x: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX,
        login_page_image_position_y: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY,
      },
    } : {});
  },

  saveLoginPageImage: async ({ file, action }) => {
    const { config } = get();
    if (!config) {
      return false;
    }
    const transform = normalizeLoginPageImageTransform({
      zoom: Number(config.login_page_image_zoom),
      positionX: Number(config.login_page_image_position_x),
      positionY: Number(config.login_page_image_position_y),
    });
    const settings: LoginPageImageSettings = {
      login_page_image_url: String(config.login_page_image_url || "").trim(),
      login_page_image_mode: normalizeLoginPageImageMode(config.login_page_image_mode),
      login_page_image_zoom: transform.zoom,
      login_page_image_position_x: transform.positionX,
      login_page_image_position_y: transform.positionY,
    };

    set({ isSavingConfig: true });
    try {
      const data = await updateLoginPageImageSettings(settings, { action, file });
      const nextConfig = normalizeConfig(data.config);
      set({ config: nextConfig });
      dispatchAppMetaUpdated({
        app_title: "1818",
        project_name: "1818",
        login_page_image_url: String(nextConfig.login_page_image_url || ""),
        login_page_image_mode: normalizeLoginPageImageMode(nextConfig.login_page_image_mode),
        login_page_image_zoom: Number(nextConfig.login_page_image_zoom),
        login_page_image_position_x: Number(nextConfig.login_page_image_position_x),
        login_page_image_position_y: Number(nextConfig.login_page_image_position_y),
      });
      toast.success("登录页图片已保存");
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存登录页图片失败");
      return false;
    } finally {
      set({ isSavingConfig: false });
    }
  },

  loadLogGovernance: async (silent = false) => {
    if (!silent) set({ isLoadingLogGovernance: true });
    try {
      const data = await fetchLogGovernance();
      set({ logGovernance: data.governance });
    } catch (error) {
      if (!silent) toast.error(error instanceof Error ? error.message : "加载日志治理数据失败");
    } finally {
      if (!silent) set({ isLoadingLogGovernance: false });
    }
  },

  cleanupLogsByRetention: async () => {
    const { config } = get();
    if (!config) {
      return;
    }
    const retentionDays = Math.min(3650, Math.max(1, Number(config.log_retention_days) || 7));
    set({ isCleaningLogs: true });
    try {
      const data = await cleanupLogs(retentionDays);
      set({
        lastLogCleanup: data.cleanup,
        logGovernance: data.governance,
      });
      toast.success(`已清理 ${data.cleanup.deleted} 条历史日志`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "清理日志失败");
    } finally {
      set({ isCleaningLogs: false });
    }
  },

  loadRegister: async (silent = false) => {
    if (!silent) set({ isLoadingRegister: true });
    try {
      const data = await fetchRegisterConfig();
      set({ registerConfig: data.register });
    } catch (error) {
      if (!silent) toast.error(error instanceof Error ? error.message : "加载注册配置失败");
    } finally {
      if (!silent) set({ isLoadingRegister: false });
    }
  },

  setRegisterConfig: (config) => {
    set({ registerConfig: config, isLoadingRegister: false });
  },

  setRegisterProxy: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, proxy: value } } : {});
  },

  setRegisterTotal: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, total: Number(value) || 0 } } : {});
  },

  setRegisterThreads: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, threads: Number(value) || 0 } } : {});
  },

  setRegisterMode: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, mode: value } } : {});
  },

  setRegisterTargetQuota: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, target_quota: Number(value) || 0 } } : {});
  },

  setRegisterTargetAvailable: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, target_available: Number(value) || 0 } } : {});
  },

  setRegisterCheckInterval: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, check_interval: Number(value) || 0 } } : {});
  },

  setRegisterMailField: (key, value) => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: { ...state.registerConfig.mail, [key]: Number(value) || 0 },
      },
    } : {});
  },

  addRegisterProvider: () => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: {
          ...state.registerConfig.mail,
          providers: [
            ...(state.registerConfig.mail.providers || []),
            { enable: true, type: "tempmail_lol", api_key: "", domain: [] },
          ],
        },
      },
    } : {});
  },

  updateRegisterProvider: (index, updates) => {
    set((state) => {
      if (!state.registerConfig) return {};
      const providers = [...(state.registerConfig.mail.providers || [])];
      providers[index] = { ...providers[index], ...updates };
      return { registerConfig: { ...state.registerConfig, mail: { ...state.registerConfig.mail, providers } } };
    });
  },

  deleteRegisterProvider: (index) => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: {
          ...state.registerConfig.mail,
          providers: (state.registerConfig.mail.providers || []).filter((_, itemIndex) => itemIndex !== index),
        },
      },
    } : {});
  },

  saveRegister: async () => {
    const { registerConfig } = get();
    if (!registerConfig) return;
    try {
      set({ isSavingRegister: true });
      const data = await updateRegisterConfig({
        mail: registerConfig.mail,
        proxy: registerConfig.proxy.trim(),
        total: Math.max(1, Number(registerConfig.total) || 1),
        threads: Math.max(1, Number(registerConfig.threads) || 1),
        mode: registerConfig.mode,
        target_quota: Math.max(1, Number(registerConfig.target_quota) || 1),
        target_available: Math.max(1, Number(registerConfig.target_available) || 1),
        check_interval: Math.max(1, Number(registerConfig.check_interval) || 5),
      });
      set({ registerConfig: data.register });
      toast.success("注册配置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存注册配置失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  toggleRegister: async () => {
    const { registerConfig } = get();
    if (!registerConfig) return;
    set({ isSavingRegister: true });
    try {
      if (!registerConfig.enabled) {
        await updateRegisterConfig({
          mail: registerConfig.mail,
          proxy: registerConfig.proxy.trim(),
          total: Math.max(1, Number(registerConfig.total) || 1),
          threads: Math.max(1, Number(registerConfig.threads) || 1),
          mode: registerConfig.mode,
          target_quota: Math.max(1, Number(registerConfig.target_quota) || 1),
          target_available: Math.max(1, Number(registerConfig.target_available) || 1),
          check_interval: Math.max(1, Number(registerConfig.check_interval) || 5),
        });
      }
      const data = registerConfig.enabled ? await stopRegister() : await startRegister();
      set({ registerConfig: data.register });
      toast.success(registerConfig.enabled ? "注册任务已停止" : "注册任务已启动");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "切换注册状态失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  resetRegister: async () => {
    set({ isSavingRegister: true });
    try {
      const data = await resetRegisterApi();
      set({ registerConfig: data.register });
      toast.success("注册统计已重置");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "重置注册统计失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  loadPools: async (silent = false) => {
    if (!silent) {
      set({ isLoadingPools: true });
    }
    try {
      const data = await fetchCPAPools();
      set({ pools: data.pools });
    } catch (error) {
      if (!silent) {
        toast.error(error instanceof Error ? error.message : "加载 CPA 连接失败");
      }
    } finally {
      if (!silent) {
        set({ isLoadingPools: false });
      }
    }
  },

  openAddDialog: () => {
    set({
      editingPool: null,
      formName: "",
      formBaseUrl: "",
      formSecretKey: "",
      showSecret: false,
      dialogOpen: true,
    });
  },

  openEditDialog: (pool) => {
    set({
      editingPool: pool,
      formName: pool.name,
      formBaseUrl: pool.base_url,
      formSecretKey: "",
      showSecret: false,
      dialogOpen: true,
    });
  },

  setDialogOpen: (open) => {
    set({ dialogOpen: open });
  },

  setFormName: (value) => {
    set({ formName: value });
  },

  setFormBaseUrl: (value) => {
    set({ formBaseUrl: value });
  },

  setFormSecretKey: (value) => {
    set({ formSecretKey: value });
  },

  setShowSecret: (checked) => {
    set({ showSecret: checked });
  },

  savePool: async () => {
    const { editingPool, formName, formBaseUrl, formSecretKey } = get();
    if (!formBaseUrl.trim()) {
      toast.error("请输入 CPA 地址");
      return;
    }
    if (!editingPool && !formSecretKey.trim()) {
      toast.error("请输入 Secret Key");
      return;
    }

    set({ isSavingPool: true });
    try {
      if (editingPool) {
        const data = await updateCPAPool(editingPool.id, {
          name: formName.trim(),
          base_url: formBaseUrl.trim(),
          secret_key: formSecretKey.trim() || undefined,
        });
        set({ pools: data.pools, dialogOpen: false });
        toast.success("连接已更新");
      } else {
        const data = await createCPAPool({
          name: formName.trim(),
          base_url: formBaseUrl.trim(),
          secret_key: formSecretKey.trim(),
        });
        set({ pools: data.pools, dialogOpen: false });
        toast.success("连接已添加");
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败");
    } finally {
      set({ isSavingPool: false });
    }
  },

  deletePool: async (pool) => {
    set({ deletingId: pool.id });
    try {
      const data = await deleteCPAPool(pool.id);
      set({ pools: data.pools });
      toast.success("连接已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除失败");
    } finally {
      set({ deletingId: null });
    }
  },

  browseFiles: async (pool) => {
    set({ loadingFilesId: pool.id });
    try {
      const data = await fetchCPAPoolFiles(pool.id);
      const files = normalizeFiles(data.files);
      set({
        browserPool: pool,
        remoteFiles: files,
        selectedNames: [],
        fileQuery: "",
        filePage: 1,
        browserOpen: true,
      });
      toast.success(`读取成功，共 ${files.length} 个远程账号`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取远程账号失败");
    } finally {
      set({ loadingFilesId: null });
    }
  },

  setBrowserOpen: (open) => {
    set({ browserOpen: open });
  },

  toggleFile: (name, checked) => {
    set((state) => {
      if (checked) {
        return {
          selectedNames: Array.from(new Set([...state.selectedNames, name])),
        };
      }
      return {
        selectedNames: state.selectedNames.filter((item) => item !== name),
      };
    });
  },

  replaceSelectedNames: (names) => {
    set({ selectedNames: Array.from(new Set(names)) });
  },

  setFileQuery: (value) => {
    set({ fileQuery: value, filePage: 1 });
  },

  setFilePage: (page) => {
    set({ filePage: page });
  },

  setPageSize: (value) => {
    set({ pageSize: value, filePage: 1 });
  },

  startImport: async () => {
    const { browserPool, selectedNames, pools } = get();
    if (!browserPool) {
      return;
    }
    if (selectedNames.length === 0) {
      toast.error("请先选择要导入的账号");
      return;
    }

    set({ isStartingImport: true });
    try {
      const result = await startCPAImport(browserPool.id, selectedNames);
      set({
        pools: pools.map((pool) =>
          pool.id === browserPool.id ? { ...pool, import_job: result.import_job } : pool,
        ),
        browserOpen: false,
      });
      toast.success("导入任务已启动");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "启动导入失败");
    } finally {
      set({ isStartingImport: false });
    }
  },
}));
