//go:build desktop

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// menuText 为托盘菜单与 macOS 应用菜单在一种界面语言下的文字
type menuText struct {
	ShowWindow, OpenInBrowser, CopyAddress, Autostart, OpenAtLogin, Quit string
	About, Services, Hide, HideOthers, ShowAll, QuitApp                  string
	File, CloseWindow                                                    string
	Edit, Undo, Redo, Cut, Copy, Paste, PasteAndMatchStyle, Delete       string
	SelectAll, View, ActualSize, ZoomIn, ZoomOut, FullScreen             string
	Window, Minimise, Zoom, BringAllToFront                              string
}

// defaultLocale 是页面报告界面语言之前菜单使用的语言
const defaultLocale = "zh-CN"

// menuTexts 为管理端各界面语言的菜单文字，macOS 菜单用词取自同语言 macOS 的系统菜单
var menuTexts = map[string]menuText{
	"zh-CN": {
		ShowWindow: "显示窗口", OpenInBrowser: "在浏览器中打开", CopyAddress: "复制 API 地址", Autostart: "开机启动", OpenAtLogin: "登录时打开", Quit: "退出",
		About: "关于 Gemini-Web2API", Services: "服务", Hide: "隐藏 Gemini-Web2API", HideOthers: "隐藏其他", ShowAll: "全部显示", QuitApp: "退出 Gemini-Web2API",
		File: "文件", CloseWindow: "关闭窗口",
		Edit: "编辑", Undo: "撤销", Redo: "重做", Cut: "剪切", Copy: "拷贝", Paste: "粘贴", PasteAndMatchStyle: "粘贴并匹配样式", Delete: "删除",
		SelectAll: "全选", View: "显示", ActualSize: "实际大小", ZoomIn: "放大", ZoomOut: "缩小", FullScreen: "进入全屏幕",
		Window: "窗口", Minimise: "最小化", Zoom: "缩放", BringAllToFront: "前置全部窗口",
	},
	"zh-TW": {
		ShowWindow: "顯示視窗", OpenInBrowser: "在瀏覽器中開啟", CopyAddress: "複製 API 位址", Autostart: "開機時啟動", OpenAtLogin: "在登入時打開", Quit: "結束",
		About: "關於 Gemini-Web2API", Services: "服務", Hide: "隱藏 Gemini-Web2API", HideOthers: "隱藏其他", ShowAll: "顯示全部", QuitApp: "結束 Gemini-Web2API",
		File: "檔案", CloseWindow: "關閉視窗",
		Edit: "編輯", Undo: "還原", Redo: "重做", Cut: "剪下", Copy: "拷貝", Paste: "貼上", PasteAndMatchStyle: "貼上並符合樣式", Delete: "刪除",
		SelectAll: "全選", View: "顯示方式", ActualSize: "實際大小", ZoomIn: "放大", ZoomOut: "縮小", FullScreen: "進入全螢幕",
		Window: "視窗", Minimise: "縮到最小", Zoom: "縮放", BringAllToFront: "將此程式所有視窗移至最前",
	},
	"en": {
		ShowWindow: "Show Window", OpenInBrowser: "Open in Browser", CopyAddress: "Copy API Address", Autostart: "Start at Sign-in", OpenAtLogin: "Open at Login", Quit: "Quit",
		About: "About Gemini-Web2API", Services: "Services", Hide: "Hide Gemini-Web2API", HideOthers: "Hide Others", ShowAll: "Show All", QuitApp: "Quit Gemini-Web2API",
		File: "File", CloseWindow: "Close Window",
		Edit: "Edit", Undo: "Undo", Redo: "Redo", Cut: "Cut", Copy: "Copy", Paste: "Paste", PasteAndMatchStyle: "Paste and Match Style", Delete: "Delete",
		SelectAll: "Select All", View: "View", ActualSize: "Actual Size", ZoomIn: "Zoom In", ZoomOut: "Zoom Out", FullScreen: "Enter Full Screen",
		Window: "Window", Minimise: "Minimize", Zoom: "Zoom", BringAllToFront: "Bring All to Front",
	},
	"ja": {
		ShowWindow: "ウインドウを表示", OpenInBrowser: "ブラウザで開く", CopyAddress: "API アドレスをコピー", Autostart: "サインイン時に起動", OpenAtLogin: "ログイン時に開く", Quit: "終了",
		About: "Gemini-Web2API について", Services: "サービス", Hide: "Gemini-Web2API を非表示", HideOthers: "ほかを非表示", ShowAll: "すべてを表示", QuitApp: "Gemini-Web2API を終了",
		File: "ファイル", CloseWindow: "ウインドウを閉じる",
		Edit: "編集", Undo: "取り消す", Redo: "やり直す", Cut: "カット", Copy: "コピー", Paste: "ペースト", PasteAndMatchStyle: "ペーストしてスタイルを合わせる", Delete: "削除",
		SelectAll: "すべてを選択", View: "表示", ActualSize: "実際のサイズ", ZoomIn: "拡大", ZoomOut: "縮小", FullScreen: "フルスクリーンにする",
		Window: "ウインドウ", Minimise: "しまう", Zoom: "拡大/縮小", BringAllToFront: "すべてを手前に移動",
	},
	"ko": {
		ShowWindow: "윈도우 보기", OpenInBrowser: "브라우저에서 열기", CopyAddress: "API 주소 복사", Autostart: "로그인할 때 시작", OpenAtLogin: "로그인 시 열기", Quit: "종료",
		About: "Gemini-Web2API에 관하여", Services: "서비스", Hide: "Gemini-Web2API 가리기", HideOthers: "기타 가리기", ShowAll: "모두 보기", QuitApp: "Gemini-Web2API 종료",
		File: "파일", CloseWindow: "윈도우 닫기",
		Edit: "편집", Undo: "실행 취소", Redo: "실행 복귀", Cut: "오려두기", Copy: "복사하기", Paste: "붙여넣기", PasteAndMatchStyle: "붙여넣고 스타일 일치", Delete: "삭제",
		SelectAll: "전체 선택", View: "보기", ActualSize: "실제 크기", ZoomIn: "확대", ZoomOut: "축소", FullScreen: "전체 화면 시작",
		Window: "윈도우", Minimise: "최소화", Zoom: "확대/축소", BringAllToFront: "모두 앞으로 가져오기",
	},
	"fr": {
		ShowWindow: "Afficher la fenêtre", OpenInBrowser: "Ouvrir dans le navigateur", CopyAddress: "Copier l’adresse de l’API", Autostart: "Lancer à l’ouverture de session", OpenAtLogin: "Ouvrir avec la session", Quit: "Quitter",
		About: "À propos de Gemini-Web2API", Services: "Services", Hide: "Masquer Gemini-Web2API", HideOthers: "Masquer les autres", ShowAll: "Tout afficher", QuitApp: "Quitter Gemini-Web2API",
		File: "Fichier", CloseWindow: "Fermer la fenêtre",
		Edit: "Édition", Undo: "Annuler", Redo: "Rétablir", Cut: "Couper", Copy: "Copier", Paste: "Coller", PasteAndMatchStyle: "Coller et adapter le style", Delete: "Supprimer",
		SelectAll: "Tout sélectionner", View: "Présentation", ActualSize: "Taille réelle", ZoomIn: "Agrandir", ZoomOut: "Réduire", FullScreen: "Activer le mode plein écran",
		Window: "Fenêtre", Minimise: "Minimiser", Zoom: "Réduire/agrandir", BringAllToFront: "Tout ramener au premier plan",
	},
	"de": {
		ShowWindow: "Fenster anzeigen", OpenInBrowser: "Im Browser öffnen", CopyAddress: "API-Adresse kopieren", Autostart: "Bei der Anmeldung starten", OpenAtLogin: "Bei der Anmeldung öffnen", Quit: "Beenden",
		About: "Über Gemini-Web2API", Services: "Dienste", Hide: "Gemini-Web2API ausblenden", HideOthers: "Andere ausblenden", ShowAll: "Alle einblenden", QuitApp: "Gemini-Web2API beenden",
		File: "Ablage", CloseWindow: "Fenster schließen",
		Edit: "Bearbeiten", Undo: "Widerrufen", Redo: "Wiederholen", Cut: "Ausschneiden", Copy: "Kopieren", Paste: "Einsetzen", PasteAndMatchStyle: "Einsetzen und Stil anpassen", Delete: "Löschen",
		SelectAll: "Alles auswählen", View: "Darstellung", ActualSize: "Originalgröße", ZoomIn: "Vergrößern", ZoomOut: "Verkleinern", FullScreen: "Vollbildmodus",
		Window: "Fenster", Minimise: "Minimieren", Zoom: "Zoomen", BringAllToFront: "Alle nach vorne bringen",
	},
}

// appMenu 返回 macOS 应用菜单：应用、文件、编辑、显示与窗口菜单，各项为系统标准操作
func appMenu(text menuText) *application.Menu {
	menu := application.NewMenu()
	app := menu.AddSubmenu("Gemini-Web2API")
	addRole(app, application.About, text.About)
	app.AddSeparator()
	addRole(app, application.ServicesMenu, text.Services)
	app.AddSeparator()
	addRole(app, application.Hide, text.Hide)
	addRole(app, application.HideOthers, text.HideOthers)
	addRole(app, application.UnHide, text.ShowAll)
	app.AddSeparator()
	addRole(app, application.Quit, text.QuitApp)
	file := menu.AddSubmenu(text.File)
	addRole(file, application.CloseWindow, text.CloseWindow)
	edit := menu.AddSubmenu(text.Edit)
	addRole(edit, application.Undo, text.Undo)
	addRole(edit, application.Redo, text.Redo)
	edit.AddSeparator()
	addRole(edit, application.Cut, text.Cut)
	addRole(edit, application.Copy, text.Copy)
	addRole(edit, application.Paste, text.Paste)
	addRole(edit, application.PasteAndMatchStyle, text.PasteAndMatchStyle)
	addRole(edit, application.Delete, text.Delete)
	addRole(edit, application.SelectAll, text.SelectAll)
	view := menu.AddSubmenu(text.View)
	addRole(view, application.ResetZoom, text.ActualSize)
	addRole(view, application.ZoomIn, text.ZoomIn)
	addRole(view, application.ZoomOut, text.ZoomOut)
	view.AddSeparator()
	addRole(view, application.ToggleFullscreen, text.FullScreen)
	addRole(menu, application.WindowMenu, text.Window)
	window := menu.FindByRole(application.WindowMenu).GetSubmenu()
	window.FindByRole(application.Minimise).SetLabel(text.Minimise)
	window.FindByRole(application.Zoom).SetLabel(text.Zoom)
	window.FindByRole(application.Front).SetLabel(text.BringAllToFront)
	return menu
}

// addRole 在 menu 末尾加入系统标准操作 role，文字为 label
func addRole(menu *application.Menu, role application.Role, label string) {
	menu.AddRole(role)
	menu.FindByRole(role).SetLabel(label)
}
