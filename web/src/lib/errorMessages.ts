import { defineMessages, fill, languages, serverText } from '@/lib/i18n'

// errorMessages 是管理 API 错误 code 在各语言的文案，{detail} 为后端返回的具体原因
const errorMessages = defineMessages({
  'zh-CN': {
    network: '无法连接服务',
    invalid_request: '请求无效：{detail}',
    account_operation_failed: '账号操作失败：{detail}',
    account_import_failed: '账号导入失败：{detail}',
    cookie_discover_failed: '无法识别 Cookie 中的账号：{detail}',
    chrome_unavailable: '无法读取 Chrome Profile：{detail}',
    invalid_config: '设置项无效：{detail}',
    config_write_failed: '写入 .env 失败：{detail}',
    usage_failed: '无法读取用量：{detail}',
    usage_records_failed: '无法读取请求记录：{detail}',
    request_body_not_found: '没有这次请求的正文：{detail}',
    request_body_failed: '无法读取请求正文：{detail}',
  },
  'zh-TW': {
    network: '無法連線到服務',
    invalid_request: '請求無效：{detail}',
    account_operation_failed: '帳號操作失敗：{detail}',
    account_import_failed: '帳號匯入失敗：{detail}',
    cookie_discover_failed: '無法識別 Cookie 中的帳號：{detail}',
    chrome_unavailable: '無法讀取 Chrome Profile：{detail}',
    invalid_config: '設定項目無效：{detail}',
    config_write_failed: '寫入 .env 失敗：{detail}',
    usage_failed: '無法讀取用量：{detail}',
    usage_records_failed: '無法讀取請求紀錄：{detail}',
    request_body_not_found: '沒有這筆請求的內文：{detail}',
    request_body_failed: '無法讀取請求內文：{detail}',
  },
  en: {
    network: 'Cannot reach the service',
    invalid_request: 'Invalid request: {detail}',
    account_operation_failed: 'Account operation failed: {detail}',
    account_import_failed: 'Account import failed: {detail}',
    cookie_discover_failed: 'Could not detect the accounts in this cookie: {detail}',
    chrome_unavailable: 'Cannot read Chrome profiles: {detail}',
    invalid_config: 'Invalid settings: {detail}',
    config_write_failed: 'Cannot write .env: {detail}',
    usage_failed: 'Cannot load usage: {detail}',
    usage_records_failed: 'Cannot load request records: {detail}',
    request_body_not_found: 'No saved body for this request: {detail}',
    request_body_failed: 'Cannot load the request body: {detail}',
  },
  ja: {
    network: 'サービスに接続できません',
    invalid_request: '無効なリクエスト：{detail}',
    account_operation_failed: 'アカウントの操作に失敗しました：{detail}',
    account_import_failed: 'アカウントのインポートに失敗しました：{detail}',
    cookie_discover_failed: 'Cookie のアカウントを識別できません：{detail}',
    chrome_unavailable: 'Chrome プロファイルを読み込めません：{detail}',
    invalid_config: '設定が無効です：{detail}',
    config_write_failed: '.env に書き込めません：{detail}',
    usage_failed: '使用量を読み込めません：{detail}',
    usage_records_failed: 'リクエスト記録を読み込めません：{detail}',
    request_body_not_found: 'このリクエストの本文は保存されていません：{detail}',
    request_body_failed: 'リクエスト本文を読み込めません：{detail}',
  },
  ko: {
    network: '서비스에 연결할 수 없습니다',
    invalid_request: '잘못된 요청: {detail}',
    account_operation_failed: '계정 작업 실패: {detail}',
    account_import_failed: '계정 가져오기 실패: {detail}',
    cookie_discover_failed: 'Cookie의 계정을 식별할 수 없습니다: {detail}',
    chrome_unavailable: 'Chrome 프로필을 읽을 수 없습니다: {detail}',
    invalid_config: '잘못된 설정: {detail}',
    config_write_failed: '.env에 쓸 수 없습니다: {detail}',
    usage_failed: '사용량을 불러올 수 없습니다: {detail}',
    usage_records_failed: '요청 기록을 불러올 수 없습니다: {detail}',
    request_body_not_found: '이 요청의 본문이 저장되지 않았습니다: {detail}',
    request_body_failed: '요청 본문을 불러올 수 없습니다: {detail}',
  },
  fr: {
    network: 'Service injoignable',
    invalid_request: 'Requête invalide : {detail}',
    account_operation_failed: 'Échec de l’opération sur le compte : {detail}',
    account_import_failed: 'Échec de l’importation du compte : {detail}',
    cookie_discover_failed: 'Impossible de détecter les comptes de ce cookie : {detail}',
    chrome_unavailable: 'Impossible de lire les profils Chrome : {detail}',
    invalid_config: 'Paramètres invalides : {detail}',
    config_write_failed: 'Impossible d’écrire .env : {detail}',
    usage_failed: 'Impossible de charger l’utilisation : {detail}',
    usage_records_failed: 'Impossible de charger les enregistrements de requêtes : {detail}',
    request_body_not_found: 'Aucun corps enregistré pour cette requête : {detail}',
    request_body_failed: 'Impossible de charger le corps de la requête : {detail}',
  },
  de: {
    network: 'Dienst nicht erreichbar',
    invalid_request: 'Ungültige Anfrage: {detail}',
    account_operation_failed: 'Kontovorgang fehlgeschlagen: {detail}',
    account_import_failed: 'Kontoimport fehlgeschlagen: {detail}',
    cookie_discover_failed: 'Die Konten in diesem Cookie konnten nicht erkannt werden: {detail}',
    chrome_unavailable: 'Chrome-Profile können nicht gelesen werden: {detail}',
    invalid_config: 'Ungültige Einstellungen: {detail}',
    config_write_failed: '.env kann nicht geschrieben werden: {detail}',
    usage_failed: 'Nutzung kann nicht geladen werden: {detail}',
    usage_records_failed: 'Anfrageprotokoll kann nicht geladen werden: {detail}',
    request_body_not_found: 'Für diese Anfrage ist kein Inhalt gespeichert: {detail}',
    request_body_failed: 'Anfrageinhalt kann nicht geladen werden: {detail}',
  },
})

// localizeError 返回 code 在当前界面语言的文案，后端原因与未知 code 的原文按服务文案翻译
export function localizeError(code: string, message: string): string {
  const language = languages.find((item) => item.code === document.documentElement.lang)?.code
  if (!language) return message
  const table: Record<string, string> = errorMessages[language]
  const template = Object.hasOwn(table, code) ? table[code] : undefined
  return template === undefined
    ? serverText(message)
    : fill(template, { detail: serverText(message) })
}
