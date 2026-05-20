use base64::Engine;
use gloo_file::futures::read_as_bytes;
use gloo_net::http::Request;
use gloo_storage::{LocalStorage, Storage};
use gloo_timers::future::TimeoutFuture;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use wasm_bindgen::JsCast;
use wasm_bindgen_futures::spawn_local;
use web_sys::{
    Event, File, HtmlAnchorElement, HtmlInputElement, HtmlSelectElement, HtmlTextAreaElement,
    window,
};
use yew::prelude::*;

const AUTH_SESSION_KEY: &str = "chatgpt2api_auth_session";
const AUTH_KEY_KEY: &str = "chatgpt2api_auth_key";
const SHARE_TEXT_PREFIX: &str = "我在 DF 使用 Image-2 生成了张图，你可以打开链接生成同款：";

#[derive(Clone, Debug, PartialEq)]
enum Page {
    Home,
    Login,
    Image,
    Invite,
    Invitation,
    Share,
    Accounts,
    Register,
    Images,
    Logs,
    Settings,
    Users,
    Profile,
}

impl Default for Page {
    fn default() -> Self {
        Page::Home
    }
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
struct Session {
    key: String,
    role: String,
    #[serde(default)]
    role_id: String,
    #[serde(default)]
    role_name: String,
    #[serde(default)]
    subject_id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    provider: String,
    #[serde(default)]
    menu_paths: Vec<String>,
    #[serde(default)]
    api_permissions: Vec<String>,
    #[serde(default)]
    image_quota_total: Option<i64>,
    #[serde(default)]
    image_quota_used: i64,
    #[serde(default)]
    image_quota_remaining: Option<i64>,
    #[serde(default)]
    invite_code: Option<String>,
    #[serde(default)]
    invite_reward_total: i64,
    #[serde(default)]
    invite_bonus_total: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct LoginResponse {
    #[serde(default)]
    token: String,
    role: String,
    #[serde(default)]
    role_id: String,
    #[serde(default)]
    role_name: String,
    subject_id: String,
    name: String,
    #[serde(default)]
    provider: String,
    #[serde(default)]
    menu_paths: Vec<String>,
    #[serde(default)]
    api_permissions: Vec<String>,
    #[serde(default)]
    image_quota_total: Option<i64>,
    #[serde(default)]
    image_quota_used: i64,
    #[serde(default)]
    image_quota_remaining: Option<i64>,
    #[serde(default)]
    invite_code: Option<String>,
    #[serde(default)]
    invite_reward_total: i64,
    #[serde(default)]
    invite_bonus_total: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct AuthProviders {
    #[serde(default)]
    registration: RegistrationProvider,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct RegistrationProvider {
    #[serde(default)]
    enabled: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ImageResponse {
    #[serde(default)]
    data: Vec<ImageItem>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct ImageItem {
    #[serde(default)]
    b64_json: String,
    #[serde(default)]
    url: String,
    #[serde(default)]
    revised_prompt: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ImageShare {
    id: String,
    share_url: String,
    image_url: String,
    #[serde(default)]
    prompt: String,
    #[serde(default)]
    revised_prompt: String,
    #[serde(default)]
    model: String,
    #[serde(default)]
    size: String,
    #[serde(default)]
    quality: String,
    #[serde(default)]
    result_index: i64,
    #[serde(default)]
    created_at: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct PublicInviteInfo {
    invite_code: String,
    invite_url: String,
    inviter_name: String,
    #[serde(default)]
    invitee_bonus_quota: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct MyInviteInfo {
    invite_code: String,
    invite_url: String,
    #[serde(default)]
    invited_count: i64,
    #[serde(default)]
    invite_reward_quota: i64,
    #[serde(default)]
    invitee_bonus_quota: i64,
    #[serde(default)]
    invite_reward_total: i64,
    #[serde(default)]
    invited_users: Vec<InvitedUser>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct InvitedUser {
    #[serde(default)]
    email: String,
    #[serde(default)]
    created_at: String,
    #[serde(default)]
    bonus_quota: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ImageCallStats {
    #[serde(default)]
    today_calls: i64,
    #[serde(default)]
    total_calls: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct CheckinState {
    #[serde(default)]
    enabled: bool,
    #[serde(default)]
    available_for_user: bool,
    #[serde(default)]
    checked_today: bool,
    #[serde(default)]
    next_reward: i64,
    #[serde(default)]
    checkin_streak: i64,
    #[serde(default)]
    checkin_total: i64,
    #[serde(default)]
    image_quota_remaining: Option<i64>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct CheckinStatusResponse {
    #[serde(flatten)]
    state: CheckinState,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct CheckinSubmitResponse {
    #[serde(default)]
    already_checked: bool,
    #[serde(default)]
    reward: i64,
    #[serde(default)]
    state: CheckinState,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct SettingsResponse {
    #[serde(default)]
    config: Value,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ManagedUsersResponse {
    #[serde(default)]
    items: Vec<Value>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct AccountsResponse {
    #[serde(default)]
    items: Vec<Value>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ManagedImagesResponse {
    #[serde(default)]
    items: Vec<Value>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct LogsResponse {
    #[serde(default)]
    items: Vec<Value>,
}

#[derive(Clone, Debug, Default, PartialEq)]
struct GeneratedImage {
    id: String,
    src: String,
    prompt: String,
    revised_prompt: String,
    model: String,
    size: String,
    quality: String,
    result_index: usize,
}

#[derive(Clone, Debug, Default, PartialEq)]
struct AppState {
    session: Option<Session>,
    page: Page,
    message: String,
    error: String,
}

fn main() {
    yew::Renderer::<App>::with_root(
        window()
            .and_then(|w| w.document())
            .and_then(|d| d.get_element_by_id("app"))
            .expect("missing #app"),
    )
    .render();
}

fn current_path() -> String {
    window()
        .and_then(|w| w.location().pathname().ok())
        .unwrap_or_else(|| "/".to_string())
}

fn current_query() -> String {
    window()
        .and_then(|w| w.location().search().ok())
        .unwrap_or_default()
}

fn page_from_path(path: &str) -> Page {
    match path.trim_end_matches('/') {
        "" | "/" => Page::Home,
        "/login" => Page::Login,
        "/image" => Page::Image,
        "/invite" => Page::Invite,
        "/invitation" => Page::Invitation,
        "/share" => Page::Share,
        "/accounts" => Page::Accounts,
        "/register" => Page::Register,
        "/image-manager" => Page::Images,
        "/logs" => Page::Logs,
        "/settings" => Page::Settings,
        "/users" => Page::Users,
        "/profile" => Page::Profile,
        _ => Page::Home,
    }
}

fn page_path(page: &Page) -> &'static str {
    match page {
        Page::Home => "/",
        Page::Login => "/login",
        Page::Image => "/image",
        Page::Invite => "/invite",
        Page::Invitation => "/invitation",
        Page::Share => "/share",
        Page::Accounts => "/accounts",
        Page::Register => "/register",
        Page::Images => "/image-manager",
        Page::Logs => "/logs",
        Page::Settings => "/settings",
        Page::Users => "/users",
        Page::Profile => "/profile",
    }
}

fn navigate_to(page: Page) {
    if let Some(history) = window().and_then(|w| w.history().ok()) {
        let _ =
            history.push_state_with_url(&wasm_bindgen::JsValue::NULL, "", Some(page_path(&page)));
    }
}

fn query_param(name: &str) -> String {
    let query = current_query();
    let query = query.trim_start_matches('?');
    for pair in query.split('&') {
        let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
        if key == name {
            return urlencoding::decode(value)
                .map(|v| v.to_string())
                .unwrap_or_default();
        }
    }
    String::new()
}

fn default_route(session: &Session) -> Page {
    if session.role == "admin" {
        Page::Accounts
    } else {
        Page::Image
    }
}

fn can_access(session: &Session, page: &Page) -> bool {
    if matches!(page, Page::Profile | Page::Image | Page::Invite) {
        return true;
    }
    if session.role == "admin" {
        return true;
    }
    session.menu_paths.iter().any(|p| p == page_path(page))
}

fn stored_session() -> Option<Session> {
    LocalStorage::get(AUTH_SESSION_KEY).ok()
}

fn store_session(session: &Session) {
    let _ = LocalStorage::set(AUTH_SESSION_KEY, session);
    let _ = LocalStorage::set(AUTH_KEY_KEY, session.key.clone());
}

fn clear_session() {
    LocalStorage::delete(AUTH_SESSION_KEY);
    LocalStorage::delete(AUTH_KEY_KEY);
}

fn session_from_login(data: LoginResponse, fallback_key: String) -> Session {
    let key = if data.token.trim().is_empty() {
        fallback_key
    } else {
        data.token
    };
    Session {
        key,
        role: data.role,
        role_id: data.role_id,
        role_name: data.role_name,
        subject_id: data.subject_id,
        name: data.name,
        provider: data.provider,
        menu_paths: data.menu_paths,
        api_permissions: data.api_permissions,
        image_quota_total: data.image_quota_total,
        image_quota_used: data.image_quota_used,
        image_quota_remaining: data.image_quota_remaining,
        invite_code: data.invite_code,
        invite_reward_total: data.invite_reward_total,
        invite_bonus_total: data.invite_bonus_total,
    }
}

async fn api_json<T: for<'de> Deserialize<'de>>(
    method: &str,
    path: &str,
    body: Option<Value>,
    token: Option<&str>,
) -> Result<T, String> {
    let mut req = match method {
        "POST" => Request::post(path),
        "PUT" => Request::put(path),
        "PATCH" => Request::patch(path),
        "DELETE" => Request::delete(path),
        _ => Request::get(path),
    };
    if let Some(token) = token {
        if !token.is_empty() {
            req = req.header("Authorization", &format!("Bearer {}", token));
        }
    }
    let resp = if let Some(body) = body {
        req.header("Content-Type", "application/json")
            .body(body.to_string())
            .map_err(|e| e.to_string())?
            .send()
            .await
            .map_err(|e| e.to_string())?
    } else {
        req.send().await.map_err(|e| e.to_string())?
    };
    let status = resp.status();
    let text = resp.text().await.unwrap_or_default();
    if !(200..300).contains(&status) {
        return Err(extract_error_message(&text, status));
    }
    serde_json::from_str(&text).map_err(|e| e.to_string())
}

async fn api_form<T: for<'de> Deserialize<'de>>(
    path: &str,
    form: web_sys::FormData,
    token: Option<&str>,
) -> Result<T, String> {
    let mut req = Request::post(path);
    if let Some(token) = token {
        if !token.is_empty() {
            req = req.header("Authorization", &format!("Bearer {}", token));
        }
    }
    let resp = req
        .body(form)
        .map_err(|e| e.to_string())?
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let status = resp.status();
    let text = resp.text().await.unwrap_or_default();
    if !(200..300).contains(&status) {
        return Err(extract_error_message(&text, status));
    }
    serde_json::from_str(&text).map_err(|e| e.to_string())
}

fn extract_error_message(text: &str, status: u16) -> String {
    if let Ok(value) = serde_json::from_str::<Value>(text) {
        for path in [
            &["detail", "error", "message"][..],
            &["detail", "message"][..],
            &["detail"][..],
            &["error", "message"][..],
            &["error"][..],
            &["message"][..],
        ] {
            if let Some(message) = value_at_path(&value, path).and_then(|v| v.as_str()) {
                if !message.trim().is_empty() {
                    return message.to_string();
                }
            }
        }
    }
    if text.trim().is_empty() {
        format!("请求失败 ({})", status)
    } else {
        text.to_string()
    }
}

fn value_at_path<'a>(value: &'a Value, path: &[&str]) -> Option<&'a Value> {
    let mut current = value;
    for key in path {
        current = current.get(*key)?;
    }
    Some(current)
}

fn first_value_text(value: &Value, paths: &[&[&str]]) -> String {
    for path in paths {
        if let Some(text) = value_at_path(value, path).and_then(|v| v.as_str()) {
            let trimmed = text.trim();
            if !trimmed.is_empty() {
                return trimmed.to_string();
            }
        }
    }
    String::new()
}

fn asset_url(value: &str) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return String::new();
    }
    if trimmed.starts_with("http://")
        || trimmed.starts_with("https://")
        || trimmed.starts_with("data:")
        || trimmed.starts_with("/")
    {
        return trimmed.to_string();
    }
    format!("/{}", trimmed.trim_start_matches('/'))
}

fn managed_image_format_label(item: &Value) -> String {
    let source = first_value_text(item, &[&["name"], &["url"], &["path"]]);
    let extension = source
        .split('?')
        .next()
        .and_then(|name| name.rsplit('.').next())
        .filter(|part| part.len() <= 5)
        .unwrap_or("image")
        .to_ascii_uppercase();
    format!("IMAGE {}", if extension == "JPEG" { "JPG" } else { &extension })
}

fn managed_image_meta(item: &Value) -> String {
    let width = item.get("width").and_then(|v| v.as_i64()).unwrap_or_default();
    let height = item.get("height").and_then(|v| v.as_i64()).unwrap_or_default();
    let size = item.get("size").and_then(|v| v.as_i64()).unwrap_or_default();
    let mut parts = Vec::<String>::new();
    if width > 0 && height > 0 {
        parts.push(format!("{} x {}", width, height));
        parts.push(format!("{:.2}MP", (width * height) as f64 / 1_000_000.0));
    }
    if size > 0 {
        parts.push(format!("{:.2} MB", size as f64 / 1_048_576.0));
    }
    parts.join(" | ")
}

fn render_managed_image_item(item: &Value) -> Html {
    let image_url = asset_url(&first_value_text(
        item,
        &[&["thumbnail_url"], &["url"], &["image_url"], &["path"]],
    ));
    let prompt = first_value_text(item, &[&["prompt"], &["revised_prompt"]]);
    let owner = first_value_text(item, &[&["owner_name"], &["owner"], &["user_name"]]);
    let created_at = first_value_text(item, &[&["created_at"], &["updated_at"]]);
    let visibility = first_value_text(item, &[&["visibility"]]);
    let meta = managed_image_meta(item);
    html! {
        <article class="managed-image-card">
            if image_url.is_empty() {
                <div class="managed-image-empty">{"无预览图"}</div>
            } else {
                <img src={image_url} alt="图库图片" loading="lazy" />
            }
            <div class="managed-image-body">
                <div class="tiny">{managed_image_format_label(item)}</div>
                if !created_at.is_empty() {
                    <div class="tiny">{created_at}</div>
                }
                if !meta.is_empty() {
                    <div class="tiny">{meta}</div>
                }
                <div class="managed-image-pills">
                    if !owner.is_empty() {
                        <span>{owner}</span>
                    }
                    if !visibility.is_empty() {
                        <span>{visibility}</span>
                    }
                </div>
                if prompt.is_empty() {
                    <p class="tiny">{"暂无提示词"}</p>
                } else {
                    <div class="prompt-scroll" tabindex="0">
                        <strong>{"完整提示词 · 可滚动查看"}</strong>
                        <span>{prompt}</span>
                    </div>
                }
            </div>
        </article>
    }
}

async fn verify_session(session: Session) -> Option<Session> {
    let data = api_json::<LoginResponse>("GET", "/auth/session", None, Some(&session.key))
        .await
        .ok()?;
    let next = session_from_login(data, session.key);
    store_session(&next);
    Some(next)
}

fn copy_to_clipboard(text: String) {
    if let Some(clipboard) = window().map(|w| w.navigator().clipboard()) {
        let _ = clipboard.write_text(&text);
    }
}

fn download_url(url: String, filename: String) {
    if let Some(document) = window().and_then(|w| w.document()) {
        if let Ok(element) = document.create_element("a") {
            if let Ok(anchor) = element.dyn_into::<HtmlAnchorElement>() {
                anchor.set_href(&url);
                anchor.set_download(&filename);
                anchor.set_target("_blank");
                if let Some(body) = document.body() {
                    let _ = body.append_child(&anchor);
                    anchor.click();
                    let _ = body.remove_child(&anchor);
                }
            }
        }
    }
}

fn event_value(event: Event) -> String {
    event
        .target()
        .and_then(|target| {
            target
                .dyn_ref::<HtmlInputElement>()
                .map(|input| input.value())
                .or_else(|| {
                    target
                        .dyn_ref::<HtmlSelectElement>()
                        .map(|select| select.value())
                })
                .or_else(|| {
                    target
                        .dyn_ref::<HtmlTextAreaElement>()
                        .map(|textarea| textarea.value())
                })
        })
        .unwrap_or_default()
}

fn textarea_value(event: Event) -> String {
    event
        .target()
        .and_then(|target| target.dyn_into::<HtmlTextAreaElement>().ok())
        .map(|input| input.value())
        .unwrap_or_default()
}

fn quota_label(session: Option<&Session>) -> String {
    match session {
        Some(session) if session.role != "admin" => session
            .image_quota_remaining
            .map(|v| v.max(0).to_string())
            .unwrap_or_else(|| "不限".to_string()),
        Some(_) => "不限".to_string(),
        None => "--".to_string(),
    }
}

fn share_copy_text(url: &str) -> String {
    format!("{}\n{}", SHARE_TEXT_PREFIX, url)
}

fn image_src(item: &ImageItem) -> String {
    if !item.url.trim().is_empty() {
        item.url.clone()
    } else if !item.b64_json.trim().is_empty() {
        format!("data:image/png;base64,{}", item.b64_json)
    } else {
        String::new()
    }
}

fn generated_image_from_response_item(
    item: &ImageItem,
    prompt: String,
    model: String,
    size: String,
    quality: String,
    result_index: usize,
) -> Option<GeneratedImage> {
    let src = image_src(item);
    if src.is_empty() {
        return None;
    }
    Some(GeneratedImage {
        id: create_id("img"),
        src,
        prompt,
        revised_prompt: item.revised_prompt.clone(),
        model,
        size,
        quality,
        result_index,
    })
}

async fn blob_from_source(src: &str) -> Result<web_sys::Blob, String> {
    let bytes = if src.starts_with("data:") {
        let payload = src.split_once(',').map(|(_, data)| data).unwrap_or("");
        base64::engine::general_purpose::STANDARD
            .decode(payload)
            .map_err(|e| e.to_string())?
    } else {
        Request::get(src)
            .send()
            .await
            .map_err(|e| e.to_string())?
            .binary()
            .await
            .map_err(|e| e.to_string())?
    };
    let array = js_sys::Array::new();
    array.push(&js_sys::Uint8Array::from(bytes.as_slice()));
    web_sys::Blob::new_with_u8_array_sequence(&array).map_err(|_| "创建图片数据失败".to_string())
}

async fn create_edit_form(
    src: &str,
    prompt: &str,
    size: &str,
    quality: &str,
    count: i64,
) -> Result<web_sys::FormData, String> {
    let blob = blob_from_source(src).await?;
    let form = web_sys::FormData::new().map_err(|_| "创建表单失败".to_string())?;
    form.append_with_blob_and_filename("image", &blob, "reference.png")
        .map_err(|_| "写入参考图失败".to_string())?;
    form.append_with_str("prompt", prompt)
        .map_err(|_| "写入提示词失败".to_string())?;
    form.append_with_str("model", "gpt-image-2")
        .map_err(|_| "写入模型失败".to_string())?;
    form.append_with_str("size", size)
        .map_err(|_| "写入尺寸失败".to_string())?;
    form.append_with_str("quality", quality)
        .map_err(|_| "写入画质失败".to_string())?;
    form.append_with_str("response_format", "url")
        .map_err(|_| "写入返回格式失败".to_string())?;
    form.append_with_str("visibility", "public")
        .map_err(|_| "写入可见性失败".to_string())?;
    form.append_with_str("n", &count.to_string())
        .map_err(|_| "写入张数失败".to_string())?;
    Ok(form)
}

fn create_id(prefix: &str) -> String {
    let random = js_sys::Math::random().to_string().replace("0.", "");
    format!("{}-{}-{}", prefix, js_sys::Date::now() as i64, random)
}

#[function_component(App)]
fn app() -> Html {
    let state = use_state(|| AppState {
        session: stored_session(),
        page: page_from_path(&current_path()),
        message: String::new(),
        error: String::new(),
    });

    {
        let state = state.clone();
        use_effect_with((), move |_| {
            if let Some(session) = (*state).session.clone() {
                spawn_local(async move {
                    if let Some(verified) = verify_session(session).await {
                        let mut next = (*state).clone();
                        next.session = Some(verified);
                        if matches!(next.page, Page::Home) {
                            next.page = next
                                .session
                                .as_ref()
                                .map(default_route)
                                .unwrap_or(Page::Login);
                            navigate_to(next.page.clone());
                        }
                        state.set(next);
                    } else {
                        clear_session();
                        let mut next = (*state).clone();
                        next.session = None;
                        if !matches!(next.page, Page::Login | Page::Invitation | Page::Share) {
                            next.page = Page::Login;
                            navigate_to(Page::Login);
                        }
                        state.set(next);
                    }
                });
            } else if matches!((*state).page, Page::Home) {
                let mut next = (*state).clone();
                next.page = Page::Login;
                navigate_to(Page::Login);
                state.set(next);
            }
            || ()
        });
    }

    let set_page = {
        let state = state.clone();
        Callback::from(move |page: Page| {
            navigate_to(page.clone());
            let mut next = (*state).clone();
            next.page = page;
            next.message.clear();
            next.error.clear();
            state.set(next);
        })
    };

    let set_session = {
        let state = state.clone();
        Callback::from(move |session: Option<Session>| {
            let mut next = (*state).clone();
            next.session = session;
            state.set(next);
        })
    };

    let set_message = {
        let state = state.clone();
        Callback::from(move |(message, error): (String, bool)| {
            let mut next = (*state).clone();
            if error {
                next.error = message;
                next.message.clear();
            } else {
                next.message = message;
                next.error.clear();
            }
            state.set(next);
        })
    };

    let logout = {
        let state = state.clone();
        Callback::from(move |_| {
            clear_session();
            let mut next = (*state).clone();
            next.session = None;
            next.page = Page::Login;
            next.message = "已退出登录".to_string();
            next.error.clear();
            navigate_to(Page::Login);
            state.set(next);
        })
    };

    let page = (*state).page.clone();
    let session = (*state).session.clone();
    let is_public = matches!(page, Page::Login | Page::Invitation | Page::Share);
    let guarded_page = if !is_public {
        match &session {
            Some(session) if can_access(session, &page) => page,
            Some(session) => default_route(session),
            None => Page::Login,
        }
    } else {
        page
    };

    html! {
        <div class="app-shell">
            if !matches!(guarded_page, Page::Login | Page::Invitation | Page::Share) {
                <TopNav session={session.clone()} page={guarded_page.clone()} set_page={set_page.clone()} logout={logout.clone()} />
            }
            if !(*state).message.is_empty() {
                <div class="notice success" style="margin-top:14px;">{(*state).message.clone()}</div>
            }
            if !(*state).error.is_empty() {
                <div class="notice error" style="margin-top:14px;">{(*state).error.clone()}</div>
            }
            {
                match guarded_page {
                    Page::Login => html! { <LoginPage on_session={set_session.clone()} on_page={set_page.clone()} on_message={set_message.clone()} /> },
                    Page::Image => html! { <ImagePage session={session.clone()} on_session={set_session.clone()} on_message={set_message.clone()} /> },
                    Page::Invite => html! { <InvitePage session={session.clone()} on_message={set_message.clone()} /> },
                    Page::Invitation => html! { <InvitationPage on_page={set_page.clone()} /> },
                    Page::Share => html! { <SharePage on_page={set_page.clone()} on_message={set_message.clone()} /> },
                    Page::Accounts => html! { <SimpleAdminPage title="号池管理" endpoint="/api/accounts" kind="accounts" session={session.clone()} /> },
                    Page::Register => html! { <RegisterPage session={session.clone()} /> },
                    Page::Images => html! { <SimpleAdminPage title="图片管理" endpoint="/api/images?scope=all" kind="images" session={session.clone()} /> },
                    Page::Logs => html! { <SimpleAdminPage title="日志管理" endpoint="/api/logs?page_size=50" kind="logs" session={session.clone()} /> },
                    Page::Settings => html! { <SettingsPage session={session.clone()} on_message={set_message.clone()} /> },
                    Page::Users => html! { <SimpleAdminPage title="用户管理" endpoint="/api/admin/users" kind="users" session={session.clone()} /> },
                    Page::Profile => html! { <ProfilePage session={session.clone()} on_message={set_message.clone()} /> },
                    Page::Home => html! {},
                }
            }
        </div>
    }
}

#[derive(Properties, PartialEq)]
struct TopNavProps {
    session: Option<Session>,
    page: Page,
    set_page: Callback<Page>,
    logout: Callback<()>,
}

#[function_component(TopNav)]
fn top_nav(props: &TopNavProps) -> Html {
    let stats = use_state(ImageCallStats::default);
    {
        let stats = stats.clone();
        let session = props.session.clone();
        use_effect_with(session, move |session| {
            if let Some(session) = session.clone() {
                spawn_local(async move {
                    loop {
                        if let Ok(data) = api_json::<ImageCallStats>(
                            "GET",
                            "/api/image-call-stats",
                            None,
                            Some(&session.key),
                        )
                        .await
                        {
                            stats.set(data);
                        }
                        TimeoutFuture::new(12_000).await;
                    }
                });
            }
            || ()
        });
    }

    let nav_items = vec![
        (Page::Image, "画图"),
        (Page::Invite, "邀请"),
        (Page::Accounts, "号池管理"),
        (Page::Register, "注册机"),
        (Page::Images, "图片管理"),
        (Page::Logs, "日志管理"),
        (Page::Settings, "设置"),
    ];
    let session = props.session.clone();
    let visible: Vec<_> = nav_items
        .into_iter()
        .filter(|(page, _)| {
            session
                .as_ref()
                .map(|s| can_access(s, page))
                .unwrap_or(false)
        })
        .collect();

    html! {
        <header class="topbar">
            <a class="brand" href="/" onclick={let set_page = props.set_page.clone(); Callback::from(move |e: MouseEvent| { e.prevent_default(); set_page.emit(Page::Image); })}>
                <img src="/logo-mark.svg" alt="DF" />
                <span class="brand-copy">
                    <span class="brand-title">{"DF IMAGE"}</span>
                    <span class="brand-subtitle">{"把想法变成作品"}</span>
                </span>
            </a>
            <nav class="nav">
                { for visible.into_iter().map(|(page, label)| {
                    let active = props.page == page;
                    let set_page = props.set_page.clone();
                    html! {
                        <button class={classes!(if active { "active" } else { "" })} onclick={Callback::from(move |_| set_page.emit(page.clone()))}>{label}</button>
                    }
                })}
            </nav>
            <div class="top-actions">
                <span class="stat-pill small" title="剩余画图额度">
                    <span class="icon-dot">{"◎"}</span>
                    <span class="stat-value">{quota_label(props.session.as_ref())}</span>
                </span>
                <button class="ghost-button" onclick={let logout = props.logout.clone(); Callback::from(move |_| logout.emit(()))}>{"退出"}</button>
                <span class="stat-pill" title="实时全站生图统计">
                    <span class="icon-dot">{"∿"}</span>
                    <span><span class="stat-label">{"今"}</span><span class="stat-value">{stats.today_calls}</span></span>
                    <span><span class="stat-label">{"总"}</span><span class="stat-value">{stats.total_calls}</span></span>
                </span>
            </div>
        </header>
    }
}

#[derive(Properties, PartialEq)]
struct LoginProps {
    on_session: Callback<Option<Session>>,
    on_page: Callback<Page>,
    on_message: Callback<(String, bool)>,
}

#[function_component(LoginPage)]
fn login_page(props: &LoginProps) -> Html {
    let username = use_state(String::new);
    let password = use_state(String::new);
    let name = use_state(String::new);
    let register_mode = use_state(|| {
        query_param("force") == "register"
            || !query_param("invite").is_empty()
            || !query_param("code").is_empty()
    });
    let registration_enabled = use_state(|| true);
    let loading = use_state(|| false);
    let invite_code = {
        let code = query_param("invite");
        if code.is_empty() {
            query_param("code")
        } else {
            code
        }
    };

    {
        let registration_enabled = registration_enabled.clone();
        use_effect_with((), move |_| {
            spawn_local(async move {
                if let Ok(data) =
                    api_json::<AuthProviders>("GET", "/auth/providers", None, None).await
                {
                    registration_enabled.set(data.registration.enabled);
                }
            });
            || ()
        });
    }

    let submit = {
        let username = username.clone();
        let password = password.clone();
        let name = name.clone();
        let register_mode = register_mode.clone();
        let loading = loading.clone();
        let on_session = props.on_session.clone();
        let on_page = props.on_page.clone();
        let on_message = props.on_message.clone();
        let invite_code = invite_code.clone();
        Callback::from(move |_| {
            let username = (*username).trim().to_string();
            let password = (*password).clone();
            let name = (*name).trim().to_string();
            let is_register = *register_mode;
            let loading = loading.clone();
            let on_session = on_session.clone();
            let on_page = on_page.clone();
            let on_message = on_message.clone();
            let invite_code = invite_code.clone();
            if username.is_empty() || password.is_empty() {
                on_message.emit(("请输入用户名和密码".to_string(), true));
                return;
            }
            loading.set(true);
            spawn_local(async move {
                let path = if is_register {
                    "/auth/register"
                } else {
                    "/auth/login"
                };
                let body = if is_register {
                    json!({"username": username, "password": password, "name": name, "invite_code": invite_code})
                } else {
                    json!({"username": username, "password": password})
                };
                match api_json::<LoginResponse>("POST", path, Some(body), None).await {
                    Ok(data) => {
                        let session = session_from_login(data, String::new());
                        store_session(&session);
                        on_session.emit(Some(session.clone()));
                        let next = default_route(&session);
                        navigate_to(next.clone());
                        on_page.emit(next);
                        on_message.emit((
                            if is_register {
                                "注册成功"
                            } else {
                                "登录成功"
                            }
                            .to_string(),
                            false,
                        ));
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                loading.set(false);
            });
        })
    };

    html! {
        <section class="login-shell">
            <div class="login-card">
                <div class="login-copy">
                    <div class="brand" style="margin-bottom:32px;">
                        <img src="/logo-mark.svg" alt="DF" />
                        <span class="brand-copy">
                            <span class="brand-title">{"DF IMAGE"}</span>
                            <span class="brand-subtitle">{"Rust Frontend"}</span>
                        </span>
                    </div>
                    <h1 class="headline">{if *register_mode { "创建账号" } else { "欢迎回来" }}</h1>
                    <p class="lead">{if *register_mode { "注册后即可进入创作台，邀请链接进入时会强制使用链接内的邀请码。" } else { "使用账号密码进入 DF 图像工作台。" }}</p>
                    if !invite_code.is_empty() {
                        <div class="notice">{"邀请码已锁定："}<strong>{invite_code.clone()}</strong></div>
                    }
                    <div class="form" style="margin-top:18px;">
                        <div class="field">
                            <label>{"用户名"}</label>
                            <input class="input" value={(*username).clone()} oninput={let username = username.clone(); Callback::from(move |e: InputEvent| username.set(event_value(e.into())))} />
                        </div>
                        if *register_mode {
                            <div class="field">
                                <label>{"昵称"}</label>
                                <input class="input" value={(*name).clone()} oninput={let name = name.clone(); Callback::from(move |e: InputEvent| name.set(event_value(e.into())))} />
                            </div>
                        }
                        <div class="field">
                            <label>{"密码"}</label>
                            <input class="input" type="password" value={(*password).clone()} oninput={let password = password.clone(); Callback::from(move |e: InputEvent| password.set(event_value(e.into())))} />
                        </div>
                        <button class="primary-button" disabled={*loading} onclick={submit}>{if *loading { "处理中..." } else if *register_mode { "注册并进入" } else { "登录" }}</button>
                        if *registration_enabled {
                            <button class="ghost-button" onclick={let register_mode = register_mode.clone(); Callback::from(move |_| register_mode.set(!*register_mode))}>
                                {if *register_mode { "已有账号，返回登录" } else { "没有账号，注册一个" }}
                            </button>
                        }
                    </div>
                </div>
                <div class="login-art"></div>
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct ImagePageProps {
    session: Option<Session>,
    on_session: Callback<Option<Session>>,
    on_message: Callback<(String, bool)>,
}

#[function_component(ImagePage)]
fn image_page(props: &ImagePageProps) -> Html {
    let prompt = use_state(String::new);
    let count = use_state(|| 1_i64);
    let size = use_state(|| "auto".to_string());
    let quality = use_state(|| "high".to_string());
    let reference_data = use_state(String::new);
    let reference_file = use_state(|| Option::<File>::None);
    let images = use_state(Vec::<GeneratedImage>::new);
    let loading = use_state(|| false);
    let apply_preset = {
        let prompt = prompt.clone();
        let size = size.clone();
        Callback::from(move |(next_prompt, next_size): (String, String)| {
            prompt.set(next_prompt);
            size.set(next_size);
        })
    };

    let on_file = {
        let reference_data = reference_data.clone();
        let reference_file = reference_file.clone();
        let on_message = props.on_message.clone();
        Callback::from(move |event: Event| {
            let files = event
                .target()
                .and_then(|target| target.dyn_into::<HtmlInputElement>().ok())
                .and_then(|input| input.files());
            if let Some(files) = files {
                if let Some(file) = files.get(0) {
                    reference_file.set(Some(file.clone()));
                    let reference_data = reference_data.clone();
                    let on_message = on_message.clone();
                    spawn_local(async move {
                        match read_file_data_url(file).await {
                            Ok(data) => reference_data.set(data),
                            Err(err) => on_message.emit((err, true)),
                        }
                    });
                }
            }
        })
    };

    let generate = {
        let prompt = prompt.clone();
        let count = count.clone();
        let size = size.clone();
        let quality = quality.clone();
        let reference_file = reference_file.clone();
        let images = images.clone();
        let loading = loading.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        let on_session = props.on_session.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                on_message.emit(("请先登录".to_string(), true));
                return;
            };
            let prompt_value = (*prompt).trim().to_string();
            if prompt_value.is_empty() {
                on_message.emit(("请输入提示词".to_string(), true));
                return;
            }
            let count_value = (*count).clamp(1, 4);
            let size_value = (*size).clone();
            let quality_value = (*quality).clone();
            let reference = (*reference_file).clone();
            let images = images.clone();
            let loading = loading.clone();
            let on_message = on_message.clone();
            let on_session = on_session.clone();
            loading.set(true);
            spawn_local(async move {
                let result = if let Some(file) = reference {
                    let form = web_sys::FormData::new().map_err(|_| "创建表单失败".to_string());
                    match form {
                        Ok(form) => {
                            let _ = form.append_with_blob("image", &file);
                            let _ = form.append_with_str("prompt", &prompt_value);
                            let _ = form.append_with_str("model", "gpt-image-2");
                            let _ = form.append_with_str("quality", &quality_value);
                            let _ = form.append_with_str("size", &size_value);
                            let _ = form.append_with_str("response_format", "url");
                            let _ = form.append_with_str("visibility", "public");
                            let _ = form.append_with_str("n", &count_value.to_string());
                            api_form::<ImageResponse>("/v1/images/edits", form, Some(&session.key))
                                .await
                        }
                        Err(err) => Err(err),
                    }
                } else {
                    api_json::<ImageResponse>(
                        "POST",
                        "/v1/images/generations",
                        Some(json!({
                            "prompt": prompt_value,
                            "model": "gpt-image-2",
                            "quality": quality_value,
                            "size": size_value,
                            "response_format": "url",
                            "visibility": "public",
                            "n": count_value,
                        })),
                        Some(&session.key),
                    )
                    .await
                };
                match result {
                    Ok(resp) => {
                        let mapped: Vec<GeneratedImage> = resp
                            .data
                            .iter()
                            .enumerate()
                            .filter_map(|(idx, item)| {
                                let src = image_src(item);
                                if src.is_empty() {
                                    None
                                } else {
                                    Some(GeneratedImage {
                                        id: create_id("img"),
                                        src,
                                        prompt: prompt_value.clone(),
                                        revised_prompt: item.revised_prompt.clone(),
                                        model: "gpt-image-2".to_string(),
                                        size: size_value.clone(),
                                        quality: quality_value.clone(),
                                        result_index: idx + 1,
                                    })
                                }
                            })
                            .collect();
                        let mut next = (*images).clone();
                        next.extend(mapped);
                        images.set(next);
                        if let Some(next) = verify_session(session).await {
                            on_session.emit(Some(next));
                        }
                        on_message.emit(("生成完成".to_string(), false));
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                loading.set(false);
            });
        })
    };

    html! {
        <section class="page">
            <div class="hero">
                <aside class="panel pad">
                    <h1 class="headline">{"把想法变成作品"}</h1>
                    <p class="lead">{"Rust 前端直连 Go 后端生成接口，失败不扣额度；分享、下载、相似生成、2K/4K 都是真实请求。"}</p>
                    <div class="preset-list">
                        { for presets().into_iter().map(|preset| {
                            let apply_preset = apply_preset.clone();
                            html! {
                                <button class="preset-button" onclick={Callback::from(move |_| apply_preset.emit((preset.1.to_string(), preset.2.to_string())))}>
                                    <strong>{preset.0}</strong>
                                    <span class="tiny">{preset.3}</span>
                                </button>
                            }
                        }) }
                    </div>
                </aside>
                <section class="panel pad">
                    <div class="form">
                        <div class="field">
                            <label>{"提示词"}</label>
                            <textarea class="textarea" placeholder="输入你想生成的画面，也可以上传参考图" value={(*prompt).clone()} oninput={let prompt = prompt.clone(); Callback::from(move |e: InputEvent| prompt.set(textarea_value(e.into())))} />
                        </div>
                        <div class="grid-3">
                            <div class="field">
                                <label>{"尺寸"}</label>
                                <select class="select" onchange={let size = size.clone(); Callback::from(move |e: Event| size.set(event_value(e)))}>
                                    <option value="auto" selected={*size == "auto"}>{"自动"}</option>
                                    <option value="1024x1024" selected={*size == "1024x1024"}>{"1:1"}</option>
                                    <option value="1536x864" selected={*size == "1536x864"}>{"16:9"}</option>
                                    <option value="864x1536" selected={*size == "864x1536"}>{"9:16"}</option>
                                    <option value="1365x1024" selected={*size == "1365x1024"}>{"4:3"}</option>
                                </select>
                            </div>
                            <div class="field">
                                <label>{"画质"}</label>
                                <select class="select" onchange={let quality = quality.clone(); Callback::from(move |e: Event| quality.set(event_value(e)))}>
                                    <option value="low" selected={*quality == "low"}>{"极速"}</option>
                                    <option value="medium" selected={*quality == "medium"}>{"标准"}</option>
                                    <option value="high" selected={*quality == "high"}>{"高清"}</option>
                                </select>
                            </div>
                            <div class="field">
                                <label>{"张数"}</label>
                                <input class="input" type="number" min="1" max="4" value={(*count).to_string()} oninput={let count = count.clone(); Callback::from(move |e: InputEvent| count.set(event_value(e.into()).parse::<i64>().unwrap_or(1).clamp(1, 4)))} />
                            </div>
                        </div>
                        <div class="button-row">
                            <label class="ghost-button" for="reference-file">{"上传参考图"}</label>
                            <input id="reference-file" class="hidden-file" type="file" accept="image/*" onchange={on_file} />
                            <button class="primary-button" disabled={*loading} onclick={generate}>{if *loading { "生成中..." } else { "立即生成" }}</button>
                        </div>
                        if !(*reference_data).is_empty() {
                            <img class="upload-preview" src={(*reference_data).clone()} alt="参考图" />
                        }
                    </div>
                </section>
            </div>
            <div style="height:22px;"></div>
            <div class="result-grid">
                { for (*images).iter().cloned().map(|image| html! {
                    <ImageCard image={image} session={props.session.clone()} on_message={props.on_message.clone()} on_append={images.clone()} />
                }) }
            </div>
        </section>
    }
}

fn presets() -> Vec<(&'static str, &'static str, &'static str, &'static str)> {
    vec![
        (
            "轮廓宇宙海报",
            "生成一张高级收藏版叙事海报，主轮廓清晰，内部世界丰富，有纸张颗粒、水彩晕染和电影级光影。",
            "864x1536",
            "高审美叙事海报、角色宇宙主题视觉。",
        ),
        (
            "青花瓷博物馆图鉴",
            "生成一张青花瓷主题博物馆图鉴式中文拆解信息图，包含材质、纹样、结构和中文标注。",
            "1365x1024",
            "文博专题、器物拆解、中文信息图。",
        ),
        (
            "古风联动宣传图",
            "生成一张古风角色联动活动宣传图，电影级光影，人物精致，宣传主视觉构图。",
            "864x1536",
            "古风角色联动、游戏活动主视觉。",
        ),
        (
            "地平线深圳实机图",
            "生成一张开放世界赛车游戏实机截图，背景深圳，现代城市天际线，真实车辆材质和速度感。",
            "1536x864",
            "次世代赛车截图、城市宣传概念图。",
        ),
    ]
}

async fn read_file_data_url(file: File) -> Result<String, String> {
    let mime = file.type_();
    let bytes = read_as_bytes(&gloo_file::File::from(file))
        .await
        .map_err(|e| e.to_string())?;
    let encoded = base64::engine::general_purpose::STANDARD.encode(bytes);
    Ok(format!(
        "data:{};base64,{}",
        if mime.is_empty() { "image/png" } else { &mime },
        encoded
    ))
}

#[derive(Properties, PartialEq)]
struct ImageCardProps {
    image: GeneratedImage,
    session: Option<Session>,
    on_message: Callback<(String, bool)>,
    on_append: UseStateHandle<Vec<GeneratedImage>>,
}

#[function_component(ImageCard)]
fn image_card(props: &ImageCardProps) -> Html {
    let busy = use_state(|| false);
    let similar_busy = use_state(|| false);
    let upscale_busy = use_state(|| false);

    let share = {
        let image = props.image.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        let busy = busy.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                on_message.emit(("请先登录".to_string(), true));
                return;
            };
            let image = image.clone();
            let on_message = on_message.clone();
            let busy = busy.clone();
            busy.set(true);
            spawn_local(async move {
                let body = json!({
                    "image": image.src,
                    "prompt": image.prompt,
                    "revised_prompt": image.revised_prompt,
                    "model": image.model,
                    "size": image.size,
                    "quality": image.quality,
                    "result_index": image.result_index,
                });
                match api_json::<ImageShare>(
                    "POST",
                    "/api/image-shares",
                    Some(body),
                    Some(&session.key),
                )
                .await
                {
                    Ok(share) => {
                        copy_to_clipboard(share_copy_text(&share.share_url));
                        on_message.emit(("分享文案已复制".to_string(), false));
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                busy.set(false);
            });
        })
    };

    let similar = {
        let image = props.image.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        let on_append = props.on_append.clone();
        let similar_busy = similar_busy.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                on_message.emit(("请先登录".to_string(), true));
                return;
            };
            let image = image.clone();
            let prompt = build_similar_prompt(&image.prompt);
            let on_message = on_message.clone();
            let on_append = on_append.clone();
            let similar_busy = similar_busy.clone();
            similar_busy.set(true);
            spawn_local(async move {
                match create_edit_form(&image.src, &prompt, &image.size, &image.quality, 1).await {
                    Ok(form) => {
                        let _ = form.append_with_str("image_resolution", "similar");
                        match api_form::<ImageResponse>(
                            "/v1/images/edits",
                            form,
                            Some(&session.key),
                        )
                        .await
                        {
                            Ok(resp) => {
                                let mut next = (*on_append).clone();
                                for (idx, item) in resp.data.iter().enumerate() {
                                    if let Some(next_image) = generated_image_from_response_item(
                                        item,
                                        prompt.clone(),
                                        "gpt-image-2".to_string(),
                                        image.size.clone(),
                                        image.quality.clone(),
                                        idx + 1,
                                    ) {
                                        next.push(next_image);
                                    }
                                }
                                on_append.set(next);
                                on_message.emit(("相似图已生成".to_string(), false));
                            }
                            Err(err) => on_message.emit((err, true)),
                        }
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                similar_busy.set(false);
            });
        })
    };

    let upscale_2k = {
        let image = props.image.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        let on_append = props.on_append.clone();
        let upscale_busy = upscale_busy.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                on_message.emit(("请先登录".to_string(), true));
                return;
            };
            let image = image.clone();
            let on_message = on_message.clone();
            let on_append = on_append.clone();
            let upscale_busy = upscale_busy.clone();
            upscale_busy.set(true);
            spawn_local(async move {
                on_message.emit(("2K 放大将消耗 2 额度".to_string(), false));
                let prompt = format!(
                    "请严格基于参考图重建同一张图的 2K 高清版本，不要改变主体、构图和核心元素，只提升分辨率、纹理、边缘细节、光影层次和整体清晰度。"
                );
                match create_edit_form(&image.src, &prompt, "2048x2048", "high", 1).await {
                    Ok(form) => {
                        let _ = form.append_with_str("image_resolution", "2k");
                        let _ = form.append_with_str("requested_size", "2048x2048");
                        match api_form::<ImageResponse>(
                            "/v1/images/edits",
                            form,
                            Some(&session.key),
                        )
                        .await
                        {
                            Ok(resp) => {
                                let mut next = (*on_append).clone();
                                for (idx, item) in resp.data.iter().enumerate() {
                                    if let Some(next_image) = generated_image_from_response_item(
                                        item,
                                        prompt.clone(),
                                        "gpt-image-2".to_string(),
                                        "2048x2048".to_string(),
                                        "high".to_string(),
                                        idx + 1,
                                    ) {
                                        next.push(next_image);
                                    }
                                }
                                on_append.set(next);
                                on_message.emit(("2K 放大完成".to_string(), false));
                            }
                            Err(err) => on_message.emit((err, true)),
                        }
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                upscale_busy.set(false);
            });
        })
    };

    let upscale_4k = {
        let image = props.image.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        let on_append = props.on_append.clone();
        let upscale_busy = upscale_busy.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                on_message.emit(("请先登录".to_string(), true));
                return;
            };
            let image = image.clone();
            let on_message = on_message.clone();
            let on_append = on_append.clone();
            let upscale_busy = upscale_busy.clone();
            upscale_busy.set(true);
            spawn_local(async move {
                on_message.emit(("4K 放大将消耗 5 额度".to_string(), false));
                let prompt = format!(
                    "请严格基于参考图重建同一张图的 4K 高清版本，不要改变主体、构图和核心元素，只提升分辨率、纹理、边缘细节、光影层次和整体清晰度。"
                );
                match create_edit_form(&image.src, &prompt, "2880x2880", "high", 1).await {
                    Ok(form) => {
                        let _ = form.append_with_str("image_resolution", "4k");
                        let _ = form.append_with_str("requested_size", "2880x2880");
                        match api_form::<ImageResponse>(
                            "/v1/images/edits",
                            form,
                            Some(&session.key),
                        )
                        .await
                        {
                            Ok(resp) => {
                                let mut next = (*on_append).clone();
                                for (idx, item) in resp.data.iter().enumerate() {
                                    if let Some(next_image) = generated_image_from_response_item(
                                        item,
                                        prompt.clone(),
                                        "gpt-image-2".to_string(),
                                        "2880x2880".to_string(),
                                        "high".to_string(),
                                        idx + 1,
                                    ) {
                                        next.push(next_image);
                                    }
                                }
                                on_append.set(next);
                                on_message.emit(("4K 放大完成".to_string(), false));
                            }
                            Err(err) => on_message.emit((err, true)),
                        }
                    }
                    Err(err) => on_message.emit((err, true)),
                }
                upscale_busy.set(false);
            });
        })
    };

    html! {
        <article class="result-card">
            <img src={props.image.src.clone()} alt="生成结果" onclick={let src = props.image.src.clone(); Callback::from(move |_| { let _ = window().and_then(|w| w.open_with_url(&src).ok()); })} />
            <div class="result-card-body">
                <div class="tiny">{format!("{} | {} | {}", props.image.model, props.image.size, props.image.quality)}</div>
                if !props.image.prompt.trim().is_empty() {
                    <div class="prompt-scroll compact" tabindex="0">
                        <strong>{"本次提示词 · 可滚动查看"}</strong>
                        <span>{props.image.prompt.clone()}</span>
                    </div>
                }
                if !props.image.revised_prompt.trim().is_empty() && props.image.revised_prompt.trim() != props.image.prompt.trim() {
                    <div class="prompt-scroll compact" tabindex="0">
                        <strong>{"模型优化提示词"}</strong>
                        <span>{props.image.revised_prompt.clone()}</span>
                    </div>
                }
                <div class="button-row">
                    <button class="ghost-button" onclick={let src = props.image.src.clone(); Callback::from(move |_| download_url(src.clone(), "df-image.png".to_string()))}>{"下载"}</button>
                    <button class="ghost-button" disabled={*busy} onclick={share}>{if *busy { "分享中" } else { "分享" }}</button>
                    <button class="ghost-button" disabled={*similar_busy} onclick={similar}>{if *similar_busy { "相似中" } else { "生成相似" }}</button>
                    <button class="ghost-button" disabled={*upscale_busy} onclick={upscale_2k}>{if *upscale_busy { "2K 中" } else { "2K 放大" }}</button>
                    <button class="ghost-button" disabled={*upscale_busy} onclick={upscale_4k}>{if *upscale_busy { "4K 中" } else { "4K 放大" }}</button>
                </div>
            </div>
        </article>
    }
}

fn build_similar_prompt(prompt: &str) -> String {
    format!(
        "请基于参考图和原始提示词生成一张同系列相似作品，但不要完全复刻参考图。保留核心主体类型、整体风格、画面质感和主要氛围；变化幅度控制在 30%-45%，至少改动构图、镜头角度、主体姿态、背景细节、光线或局部道具中的 4 项，并加入新的视觉焦点，避免输出几乎一模一样的结果。\n原始提示词：{}",
        prompt.trim()
    )
}

#[derive(Properties, PartialEq)]
struct InviteProps {
    session: Option<Session>,
    on_message: Callback<(String, bool)>,
}

#[function_component(InvitePage)]
fn invite_page(props: &InviteProps) -> Html {
    let info = use_state(|| Option::<MyInviteInfo>::None);
    let loading = use_state(|| true);
    {
        let info = info.clone();
        let loading = loading.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        use_effect_with(session, move |session| {
            if let Some(session) = session.clone() {
                spawn_local(async move {
                    match api_json::<MyInviteInfo>(
                        "GET",
                        "/api/invite/me",
                        None,
                        Some(&session.key),
                    )
                    .await
                    {
                        Ok(data) => info.set(Some(data)),
                        Err(err) => on_message.emit((err, true)),
                    }
                    loading.set(false);
                });
            }
            || ()
        });
    }
    let copy_invite = {
        let info = info.clone();
        let on_message = props.on_message.clone();
        Callback::from(move |_| {
            if let Some(info) = (*info).clone() {
                let bonus = if info.invitee_bonus_quota > 0 {
                    format!("，注册额外领取 {} 点额度", info.invitee_bonus_quota)
                } else {
                    String::new()
                };
                copy_to_clipboard(format!(
                    "我在 DF 使用 Image-2 生成图片，邀请你一起创作{}：{}",
                    bonus, info.invite_url
                ));
                on_message.emit(("邀请文案已复制".to_string(), false));
            }
        })
    };

    html! {
        <section class="page">
            <div class="panel pad">
                <h1 class="headline">{"邀请注册，双方都拿创作点数"}</h1>
                <p class="lead">{"好友通过你的邀请页点击注册时，注册页会自动锁定邀请码。后台配置的奖励会在注册成功后立即发放。"}</p>
                if *loading {
                    <p class="muted">{"加载中..."}</p>
                } else if let Some(info) = (*info).clone() {
                    <div class="grid-2">
                        <div>
                            <div class="code-box">{info.invite_code.clone()}</div>
                            <p class="tiny">{info.invite_url.clone()}</p>
                            <div class="button-row">
                                <button class="primary-button" onclick={copy_invite}>{"复制邀请"}</button>
                                <a class="ghost-button" href={format!("/invitation?code={}", urlencoding::encode(&info.invite_code))}>{"预览邀请页"}</a>
                            </div>
                        </div>
                        <div class="grid-3">
                            <div class="panel pad"><p class="muted">{"已邀请"}</p><h2>{info.invited_count}</h2></div>
                            <div class="panel pad"><p class="muted">{"每邀请奖励"}</p><h2>{info.invite_reward_quota}</h2></div>
                            <div class="panel pad"><p class="muted">{"累计奖励"}</p><h2>{info.invite_reward_total}</h2></div>
                        </div>
                    </div>
                    <div class="table-list" style="margin-top:18px;">
                        { for info.invited_users.into_iter().map(|item| html! {
                            <div class="list-item">
                                <strong>{item.email}</strong>
                                <span class="tiny">{format!("{} | 奖励 {}", item.created_at, item.bonus_quota)}</span>
                            </div>
                        }) }
                    </div>
                }
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct InvitationProps {
    on_page: Callback<Page>,
}

#[function_component(InvitationPage)]
fn invitation_page(_props: &InvitationProps) -> Html {
    let info = use_state(|| Option::<PublicInviteInfo>::None);
    let loading = use_state(|| true);
    let code = {
        let code = query_param("code");
        if code.is_empty() {
            query_param("invite")
        } else {
            code
        }
    };
    {
        let info = info.clone();
        let loading = loading.clone();
        let code = code.clone();
        use_effect_with(code, move |code| {
            let code = code.clone();
            spawn_local(async move {
                if !code.is_empty() {
                    if let Ok(data) = api_json::<PublicInviteInfo>(
                        "GET",
                        &format!("/auth/invite/{}", urlencoding::encode(&code)),
                        None,
                        None,
                    )
                    .await
                    {
                        info.set(Some(data));
                    }
                }
                loading.set(false);
            });
            || ()
        });
    }
    html! {
        <section class="public-shell">
            <div class="public-card panel pad">
                if *loading {
                    <p class="muted">{"加载邀请信息..."}</p>
                } else if let Some(info) = (*info).clone() {
                    <h1 class="headline">{"领取邀请额度，开始生成第一张图"}</h1>
                    <p class="lead">{format!("{} 邀请你使用 DF Image-2 创作，被邀请人额外获得 {} 点额度。", info.inviter_name, info.invitee_bonus_quota)}</p>
                    <div class="code-box">{info.invite_code.clone()}</div>
                    <div class="button-row" style="margin-top:20px;">
                        <a class="primary-button" href={format!("/login?invite={}&force=register", urlencoding::encode(&info.invite_code))}>{"立即注册领取"}</a>
                        <button class="ghost-button" onclick={let text = format!("{} 邀请你使用 DF Image-2 创作：{}", info.inviter_name, info.invite_url); Callback::from(move |_| copy_to_clipboard(text.clone()))}>{"复制邀请"}</button>
                    </div>
                } else {
                    <h1 class="headline">{"邀请链接无效"}</h1>
                    <p class="lead">{"该邀请码不存在或已不可用，请让邀请人重新发送邀请链接。"}</p>
                    <a class="primary-button" href="/login">{"返回登录"}</a>
                }
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct ShareProps {
    on_page: Callback<Page>,
    on_message: Callback<(String, bool)>,
}

#[function_component(SharePage)]
fn share_page(props: &ShareProps) -> Html {
    let share = use_state(|| Option::<ImageShare>::None);
    let error = use_state(String::new);
    let loading = use_state(|| true);
    let id = query_param("id");
    {
        let share = share.clone();
        let error = error.clone();
        let loading = loading.clone();
        let id = id.clone();
        use_effect_with(id, move |id| {
            let id = id.clone();
            spawn_local(async move {
                if id.is_empty() {
                    error.set("链接里没有带上分享 ID".to_string());
                } else {
                    match api_json::<ImageShare>(
                        "GET",
                        &format!("/api/image-shares/{}", urlencoding::encode(&id)),
                        None,
                        None,
                    )
                    .await
                    {
                        Ok(data) => share.set(Some(data)),
                        Err(err) => error.set(err),
                    }
                }
                loading.set(false);
            });
            || ()
        });
    }
    html! {
        <section class="page">
            if *loading {
                <div class="panel pad"><p class="muted">{"正在加载分享页..."}</p></div>
            } else if let Some(share) = (*share).clone() {
                <div class="share-layout">
                    <div class="panel share-image"><img src={share.image_url.clone()} alt="分享图片" /></div>
                    <aside class="panel pad">
                        <h1>{"这张图来自 DF 图像工作台"}</h1>
                        <p class="tiny">{format!("{} | {} | {} | 结果 {}", share.model, share.size, share.quality, share.result_index)}</p>
                        <div class="notice">{share.prompt.clone()}</div>
                        if !share.revised_prompt.is_empty() {
                            <p class="muted">{share.revised_prompt.clone()}</p>
                        }
                        <div class="button-row">
                            <button class="primary-button" onclick={let url = share.share_url.clone(); let on_message = props.on_message.clone(); Callback::from(move |_| { copy_to_clipboard(share_copy_text(&url)); on_message.emit(("分享文案已复制".to_string(), false)); })}>{"复制分享链接"}</button>
                            <button class="ghost-button" onclick={let url = share.image_url.clone(); Callback::from(move |_| copy_to_clipboard(url.clone()))}>{"复制图片地址"}</button>
                            <button class="ghost-button" onclick={let url = share.image_url.clone(); Callback::from(move |_| download_url(url.clone(), "df-share.png".to_string()))}>{"下载图片"}</button>
                            <a class="ghost-button" href={share.image_url.clone()} target="_blank">{"查看原图"}</a>
                        </div>
                    </aside>
                </div>
            } else {
                <div class="panel pad"><h1>{"这个分享页没有图片"}</h1><p class="lead">{(*error).clone()}</p></div>
            }
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct SimpleAdminProps {
    title: &'static str,
    endpoint: &'static str,
    kind: &'static str,
    session: Option<Session>,
}

#[function_component(SimpleAdminPage)]
fn simple_admin_page(props: &SimpleAdminProps) -> Html {
    let items = use_state(Vec::<Value>::new);
    let loading = use_state(|| true);
    let error = use_state(String::new);
    {
        let items = items.clone();
        let loading = loading.clone();
        let error = error.clone();
        let session = props.session.clone();
        let endpoint = props.endpoint.to_string();
        let kind = props.kind.to_string();
        use_effect_with(
            (session, endpoint, kind),
            move |(session, endpoint, kind)| {
                let session = session.clone();
                let endpoint = endpoint.clone();
                let kind = kind.clone();
                spawn_local(async move {
                    if let Some(session) = session {
                        let result: Result<Vec<Value>, String> = match kind.as_str() {
                            "accounts" => api_json::<AccountsResponse>(
                                "GET",
                                &endpoint,
                                None,
                                Some(&session.key),
                            )
                            .await
                            .map(|d| d.items),
                            "users" => api_json::<ManagedUsersResponse>(
                                "GET",
                                &endpoint,
                                None,
                                Some(&session.key),
                            )
                            .await
                            .map(|d| d.items),
                            "images" => api_json::<ManagedImagesResponse>(
                                "GET",
                                &endpoint,
                                None,
                                Some(&session.key),
                            )
                            .await
                            .map(|d| d.items),
                            "logs" => {
                                api_json::<LogsResponse>("GET", &endpoint, None, Some(&session.key))
                                    .await
                                    .map(|d| d.items)
                            }
                            _ => Ok(vec![]),
                        };
                        match result {
                            Ok(data) => items.set(data),
                            Err(err) => error.set(err),
                        }
                    }
                    loading.set(false);
                });
                || ()
            },
        );
    }
    html! {
        <section class="page">
            <div class="panel pad">
                <h1>{props.title}</h1>
                if *loading {
                    <p class="muted">{"加载中..."}</p>
                } else if !(*error).is_empty() {
                    <div class="notice error">{(*error).clone()}</div>
                } else {
                    if props.kind == "images" {
                        <div class="mobile-update-banner">
                            <strong>{"手机图库已升级"}</strong>
                            <span>{"公开图片以卡片方式展示，点击提示词区域即可在手机上完整滚动查看，适合复用爆款提示词和检查入库结果。"}</span>
                        </div>
                        <p class="muted">{format!("共 {} 张图片，当前展示最近 80 张。", items.len())}</p>
                        <div class="image-gallery-grid">
                            { for (*items).iter().take(80).map(render_managed_image_item) }
                        </div>
                    } else {
                        <p class="muted">{format!("共 {} 条。Rust 前端已接真实接口，详细编辑功能后续按模块继续补。", items.len())}</p>
                        <div class="table-list">
                            { for (*items).iter().take(80).map(|item| html! {
                                <pre class="list-item tiny">{serde_json::to_string_pretty(item).unwrap_or_default()}</pre>
                            }) }
                        </div>
                    }
                }
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct SettingsProps {
    session: Option<Session>,
    on_message: Callback<(String, bool)>,
}

#[function_component(SettingsPage)]
fn settings_page(props: &SettingsProps) -> Html {
    let config = use_state(String::new);
    let loading = use_state(|| true);
    {
        let config = config.clone();
        let loading = loading.clone();
        let session = props.session.clone();
        use_effect_with(session, move |session| {
            if let Some(session) = session.clone() {
                spawn_local(async move {
                    if let Ok(data) = api_json::<SettingsResponse>(
                        "GET",
                        "/api/settings",
                        None,
                        Some(&session.key),
                    )
                    .await
                    {
                        config.set(serde_json::to_string_pretty(&data.config).unwrap_or_default());
                    }
                    loading.set(false);
                });
            }
            || ()
        });
    }
    let save = {
        let config = config.clone();
        let session = props.session.clone();
        let on_message = props.on_message.clone();
        Callback::from(move |_| {
            let Some(session) = session.clone() else {
                return;
            };
            let body = serde_json::from_str::<Value>(&config).unwrap_or(Value::Null);
            if !body.is_object() {
                on_message.emit(("配置必须是 JSON 对象".to_string(), true));
                return;
            }
            let on_message = on_message.clone();
            spawn_local(async move {
                match api_json::<SettingsResponse>(
                    "POST",
                    "/api/settings",
                    Some(body),
                    Some(&session.key),
                )
                .await
                {
                    Ok(_) => on_message.emit(("设置已保存".to_string(), false)),
                    Err(err) => on_message.emit((err, true)),
                }
            });
        })
    };
    html! {
        <section class="page">
            <div class="panel pad">
                <h1>{"设置"}</h1>
                if *loading {
                    <p class="muted">{"加载中..."}</p>
                } else {
                    <textarea class="textarea" style="min-height:520px;font-family:monospace;" value={(*config).clone()} oninput={let config = config.clone(); Callback::from(move |e: InputEvent| config.set(textarea_value(e.into())))} />
                    <div class="button-row" style="margin-top:14px;"><button class="primary-button" onclick={save}>{"保存配置"}</button></div>
                }
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct RegisterProps {
    session: Option<Session>,
}

#[function_component(RegisterPage)]
fn register_page(_props: &RegisterProps) -> Html {
    html! {
        <section class="page">
            <div class="panel pad">
                <h1>{"注册机"}</h1>
                <p class="lead">{"Rust 前端已切入主站。批量注册机的复杂 SSE 控制台后续继续用 Go 接口重写；当前后台接口仍保留，未迁移补 RT 项目。"}</p>
            </div>
        </section>
    }
}

#[derive(Properties, PartialEq)]
struct ProfileProps {
    session: Option<Session>,
    on_message: Callback<(String, bool)>,
}

#[function_component(ProfilePage)]
fn profile_page(props: &ProfileProps) -> Html {
    let Some(session) = props.session.clone() else {
        return html! {};
    };
    html! {
        <section class="page">
            <div class="panel pad">
                <h1>{"个人中心"}</h1>
                <div class="grid-3">
                    <div class="panel pad"><p class="muted">{"用户名"}</p><h2>{session.name.clone()}</h2></div>
                    <div class="panel pad"><p class="muted">{"角色"}</p><h2>{session.role.clone()}</h2></div>
                    <div class="panel pad"><p class="muted">{"剩余额度"}</p><h2>{quota_label(Some(&session))}</h2></div>
                </div>
                if let Some(code) = session.invite_code {
                    <div class="notice" style="margin-top:18px;">{"邀请码："}<strong>{code}</strong></div>
                }
            </div>
        </section>
    }
}
