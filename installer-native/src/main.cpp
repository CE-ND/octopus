#include "install.h"
#include "../resource.h"
#include "generated_config.h"
#include <windowsx.h>
#include <commctrl.h>
#include <d2d1.h>
#include <dwrite.h>
#include <dwmapi.h>
#include <shellapi.h>
#include <shobjidl.h>
#include <wincodec.h>
#include <wrl/client.h>
#include <algorithm>
#include <cmath>
#include <fstream>
#include <memory>
#include <thread>
#include <vector>

using Microsoft::WRL::ComPtr;
namespace {
constexpr float Width = 840, Height = 560;
constexpr UINT UpdateMessage = WM_APP + 1;
enum ControlId { Primary = 1001, Browse, Shortcut, Advanced, Minimize, Close, PathInput, CurrentUser, AllUsers, LogLink };
enum class Page { Welcome, Installing, Complete, Failed };
struct Arguments {
  bool preview = false;
  std::wstring previewState;
  std::wstring smokeReport;
  std::wstring selfTest;
  float previewScale = 0;
};

D2D1_COLOR_F Color(UINT rgb, float opacity = 1) { return D2D1::ColorF(rgb, opacity); }
const auto Ink = Color(0x252b2c), Muted = Color(0x74797c), Green = Color(0x4e8564);
const auto White = Color(0xffffff), Outline = Color(0xe1e2e5), Field = Color(0xf5f5f6);

class Window {
 public:
  Window(HINSTANCE instance, Arguments args) : instance_(instance), args_(std::move(args)), started_(GetTickCount64()) {}
  ~Window() { if (worker_.joinable()) worker_.join(); if (editFont_) DeleteObject(editFont_); if (fieldBrush_) DeleteObject(fieldBrush_); }
  int Run(int show) {
    InitCommonControls();
    if (FAILED(D2D1CreateFactory(D2D1_FACTORY_TYPE_SINGLE_THREADED, factory_.GetAddressOf()))) return 1;
    if (FAILED(DWriteCreateFactory(DWRITE_FACTORY_TYPE_SHARED, __uuidof(IDWriteFactory),
        reinterpret_cast<IUnknown**>(write_.GetAddressOf())))) return 1;
    ComPtr<IDWriteFontCollection> fonts;
    if (SUCCEEDED(write_->GetSystemFontCollection(fonts.GetAddressOf(), FALSE))) {
      for (const auto* candidate : {L"Microsoft YaHei", L"Microsoft YaHei UI", L"Noto Sans SC", L"Segoe UI"}) {
        UINT32 index = 0;
        BOOL exists = FALSE;
        fonts->FindFamilyName(candidate, &index, &exists);
        if (exists) { fontFamily_ = candidate; break; }
      }
    }
    if (FAILED(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER,
        IID_PPV_ARGS(wic_.GetAddressOf())))) return 1;
    const auto properties = D2D1::RenderTargetProperties(D2D1_RENDER_TARGET_TYPE_DEFAULT,
        D2D1::PixelFormat(DXGI_FORMAT_B8G8R8A8_UNORM, D2D1_ALPHA_MODE_IGNORE), 96, 96);
    if (FAILED(factory_->CreateDCRenderTarget(&properties, target_.GetAddressOf()))) return 1;
    LoadBackground();
    scale_ = args_.previewScale > 0 ? args_.previewScale : static_cast<float>(GetDpiForSystem()) / 96;
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = Procedure;
    wc.hInstance = instance_;
    wc.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    wc.hIcon = LoadIconW(instance_, MAKEINTRESOURCEW(IDI_SETUP));
    wc.hIconSm = wc.hIcon;
    wc.lpszClassName = L"OctopusNativeSetup";
    wc.style = CS_DROPSHADOW;
    RegisterClassExW(&wc);
    RECT work{};
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    scale_ = std::min(scale_, std::min((work.right-work.left-24)/Width, (work.bottom-work.top-24)/Height));
    const int w = Px(Width), h = Px(Height);
    window_ = CreateWindowExW(WS_EX_APPWINDOW, wc.lpszClassName, L"Octopus 安装向导",
        WS_POPUP | WS_MINIMIZEBOX | WS_SYSMENU | WS_CLIPCHILDREN,
        work.left + (work.right - work.left - w) / 2, work.top + (work.bottom - work.top - h) / 2,
        w, h, nullptr, nullptr, instance_, this);
    if (!window_) return 1;
    ShowWindow(window_, args_.smokeReport.empty() ? show : SW_SHOWNOACTIVATE);
    UpdateWindow(window_);
    MSG message{};
    while (GetMessageW(&message, nullptr, 0, 0) > 0) {
      if (message.message == WM_KEYDOWN && message.wParam == VK_ESCAPE) { RequestClose(); continue; }
      if (message.message == WM_KEYDOWN && message.wParam == VK_RETURN && GetFocus() == path_) { Act(Primary); continue; }
      if (!IsDialogMessageW(window_, &message)) { TranslateMessage(&message); DispatchMessageW(&message); }
    }
    return static_cast<int>(message.wParam);
  }

 private:
  static LRESULT CALLBACK Procedure(HWND hwnd, UINT message, WPARAM w, LPARAM l) {
    auto self = reinterpret_cast<Window*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
    if (message == WM_NCCREATE) {
      self = static_cast<Window*>(reinterpret_cast<CREATESTRUCTW*>(l)->lpCreateParams);
      self->window_ = hwnd;
      SetWindowLongPtrW(hwnd, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(self));
    }
    return self ? self->HandleMessage(message, w, l) : DefWindowProcW(hwnd, message, w, l);
  }

  static LRESULT CALLBACK CheckProcedure(HWND hwnd, UINT message, WPARAM w, LPARAM l, UINT_PTR, DWORD_PTR reference) {
    auto self = reinterpret_cast<Window*>(reference);
    if (message == WM_PAINT) {
      PAINTSTRUCT paint{};
      HDC dc = BeginPaint(hwnd, &paint);
      RECT rect{};
      GetClientRect(hwnd, &rect);
      self->DrawControl(hwnd, dc, rect, false, GetFocus() == hwnd);
      EndPaint(hwnd, &paint);
      return 0;
    }
    if (message == WM_ERASEBKGND) return 1;
    if (message == WM_SETFOCUS || message == WM_KILLFOCUS) InvalidateRect(hwnd, nullptr, FALSE);
    return DefSubclassProc(hwnd, message, w, l);
  }

  LRESULT HandleMessage(UINT message, WPARAM w, LPARAM l) {
    switch (message) {
      case WM_CREATE: CreateControls(); return 0;
      case WM_ERASEBKGND: return 1;
      case WM_PAINT: Paint(); return 0;
      case WM_NCHITTEST: {
        POINT point{GET_X_LPARAM(l), GET_Y_LPARAM(l)};
        ScreenToClient(window_, &point);
        if (point.y < Px(78) && point.x < Px(738)) return HTCAPTION;
        return HTCLIENT;
      }
      case WM_COMMAND:
        if (HIWORD(w) == BN_CLICKED) Act(LOWORD(w));
        return 0;
      case WM_DRAWITEM: {
        const auto draw = reinterpret_cast<DRAWITEMSTRUCT*>(l);
        DrawControl(draw->hwndItem, draw->hDC, draw->rcItem,
                    (draw->itemState & ODS_SELECTED) != 0,
                    (draw->itemState & ODS_FOCUS) != 0 && (draw->itemState & ODS_NOFOCUSRECT) == 0);
        return TRUE;
      }
      case WM_CTLCOLOREDIT:
        SetTextColor(reinterpret_cast<HDC>(w), RGB(109,114,117));
        SetBkColor(reinterpret_cast<HDC>(w), RGB(245,245,246));
        return reinterpret_cast<LRESULT>(fieldBrush_);
      case WM_DPICHANGED: {
        if (args_.previewScale <= 0) scale_ = static_cast<float>(HIWORD(w)) / 96;
        const auto suggested = reinterpret_cast<RECT*>(l);
        MONITORINFO monitor{};
        monitor.cbSize = sizeof(monitor);
        GetMonitorInfoW(MonitorFromRect(suggested, MONITOR_DEFAULTTONEAREST), &monitor);
        const auto work = monitor.rcWork;
        scale_ = std::min(scale_, std::min((work.right-work.left-24)/Width, (work.bottom-work.top-24)/Height));
        const int x = std::clamp(static_cast<int>(suggested->left), static_cast<int>(work.left), static_cast<int>(work.right)-Px(Width));
        const int y = std::clamp(static_cast<int>(suggested->top), static_cast<int>(work.top), static_cast<int>(work.bottom)-Px(Height));
        SetWindowPos(window_, nullptr, x, y, Px(Width), Px(Height), SWP_NOZORDER | SWP_NOACTIVATE);
        Layout();
        return 0;
      }
      case WM_SIZE: RoundWindow(); return 0;
      case WM_CLOSE: RequestClose(); return 0;
      case WM_TIMER:
        if (w == 2) { KillTimer(window_, 2); DestroyWindow(window_); return 0; }
        if (w == 1) { InvalidateRect(window_, nullptr, FALSE); return 0; }
        break;
      case DM_GETDEFID: return MAKELRESULT(Primary, DC_HASDEFID);
      case UpdateMessage: {
        std::unique_ptr<setup::Update> update(reinterpret_cast<setup::Update*>(l));
        Receive(*update);
        return 0;
      }
      case WM_DESTROY:
        KillTimer(window_, 1);
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcW(window_, message, w, l);
  }

  int Px(float value) const { return static_cast<int>(std::lround(value * scale_)); }
  HWND Button(int id, const wchar_t* title, DWORD style = BS_OWNERDRAW) {
    HWND handle = CreateWindowExW(0, L"BUTTON", title, WS_CHILD | WS_VISIBLE | WS_TABSTOP | style,
        0, 0, 1, 1, window_, reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)), instance_, nullptr);
    if (style != BS_OWNERDRAW) SetWindowSubclass(handle, CheckProcedure, 1, reinterpret_cast<DWORD_PTR>(this));
    return handle;
  }

  void CreateControls() {
    fieldBrush_ = CreateSolidBrush(RGB(245,245,246));
    try { options_.directory = setup::DefaultDirectory(false); }
    catch (...) { options_.directory = L"C:\\Octopus"; }
    path_ = CreateWindowExW(0, L"EDIT", options_.directory.c_str(), WS_CHILD | WS_VISIBLE | WS_TABSTOP | ES_AUTOHSCROLL,
        0,0,1,1,window_,reinterpret_cast<HMENU>(PathInput),instance_,nullptr);
    SendMessageW(path_, EM_SETLIMITTEXT, 230, 0);
    Button(Browse, L"选择安装目录");
    Button(Primary, L"一键安装");
    Button(Shortcut, L"创建桌面快捷方式", BS_AUTOCHECKBOX | WS_GROUP);
    SendDlgItemMessageW(window_, Shortcut, BM_SETCHECK, BST_CHECKED, 0);
    Button(Advanced, L"自定义安装");
    Button(CurrentUser, L"仅当前用户", BS_AUTORADIOBUTTON | WS_GROUP);
    Button(AllUsers, L"所有用户", BS_AUTORADIOBUTTON);
    SendDlgItemMessageW(window_, CurrentUser, BM_SETCHECK, BST_CHECKED, 0);
    Button(LogLink, L"查看安装日志");
    Button(Minimize, L"最小化");
    Button(Close, L"关闭");
    if (args_.previewState == L"advanced") advanced_ = true;
    if (args_.previewState == L"progress") { page_ = Page::Installing; message_ = L"正在安装 Octopus，请稍候"; SetTimer(window_,1,30,nullptr); }
    if (args_.previewState == L"success") page_ = Page::Complete;
    if (args_.previewState == L"error") { page_ = Page::Failed; message_ = L"无法写入所选目录，请检查目录权限，或返回选择其他位置。"; }
    Layout();
  }

  void Place(int id, float x, float y, float width, float height) {
    SetWindowPos(GetDlgItem(window_,id), nullptr, Px(x),Px(y),Px(width),Px(height), SWP_NOZORDER | SWP_NOACTIVATE);
  }
  void Show(int id, bool visible) { ShowWindow(GetDlgItem(window_, id), visible ? SW_SHOW : SW_HIDE); }

  void Layout() {
    if (editFont_) DeleteObject(editFont_);
    editFont_ = CreateFontW(-Px(17),0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,
                          OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH,fontFamily_.c_str());
    SendMessageW(path_, WM_SETFONT, reinterpret_cast<WPARAM>(editFont_), TRUE);
    Place(PathInput,252,368,404,26);
    Place(Browse,658,359,42,42);
    Place(Primary,130,421,580,54);
    Place(Shortcut,130,496,310,30);
    Place(Advanced,574,496,136,30);
    Place(CurrentUser,130,216,280,46);
    Place(AllUsers,430,216,280,46);
    Place(LogLink,130,496,180,30);
    Place(Minimize,740,19,35,34);
    Place(Close,783,19,35,34);
    Refresh();
    RoundWindow();
  }

  void RoundWindow() {
    if (!window_) return;
    RECT rect{};
    GetClientRect(window_, &rect);
    HRGN region = CreateRoundRectRgn(0,0,rect.right + 1,rect.bottom + 1,Px(32),Px(32));
    if (region && !SetWindowRgn(window_,region,TRUE)) DeleteObject(region);
  }

  void Refresh() {
    const bool welcome = page_ == Page::Welcome;
    Show(PathInput,welcome); Show(Browse,welcome); Show(Shortcut,welcome);
    Show(CurrentUser,welcome && advanced_); Show(AllUsers,welcome && advanced_);
    Show(Advanced,page_ != Page::Installing);
    Show(LogLink,!welcome && page_ != Page::Installing && !logPath_.empty());
    SetDlgItemTextW(window_,Advanced, page_ == Page::Complete ? L"完成" : page_ == Page::Failed ? L"返回设置" : advanced_ ? L"收起选项" : L"自定义安装");
    SetDlgItemTextW(window_,Primary,page_ == Page::Complete ? L"启动 Octopus" : page_ == Page::Failed ? L"重试安装" : page_ == Page::Installing ? L"正在安装" : args_.preview ? L"一键安装 · 预览" : L"一键安装");
    EnableWindow(GetDlgItem(window_,Primary),page_ != Page::Installing);
    InvalidateRect(window_,nullptr,FALSE);
    for (int id : {Primary,Advanced,Shortcut,CurrentUser,AllUsers,LogLink}) InvalidateRect(GetDlgItem(window_,id),nullptr,FALSE);
  }

  void LoadBackground() {
    const HRSRC resource = FindResourceW(instance_,MAKEINTRESOURCEW(IDR_BACKGROUND),RT_RCDATA);
    if (!resource) throw std::runtime_error("缺少安装界面背景资源。");
    const auto bytes = SizeofResource(instance_,resource);
    auto data = static_cast<BYTE*>(LockResource(LoadResource(instance_,resource)));
    ComPtr<IWICStream> stream;
    ComPtr<IWICBitmapDecoder> decoder;
    ComPtr<IWICBitmapFrameDecode> frame;
    ComPtr<IWICFormatConverter> converter;
    HRESULT result = wic_->CreateStream(stream.GetAddressOf());
    if (SUCCEEDED(result)) result = stream->InitializeFromMemory(data,bytes);
    if (SUCCEEDED(result)) result = wic_->CreateDecoderFromStream(stream.Get(),nullptr,WICDecodeMetadataCacheOnLoad,decoder.GetAddressOf());
    if (SUCCEEDED(result)) result = decoder->GetFrame(0,frame.GetAddressOf());
    if (SUCCEEDED(result)) result = wic_->CreateFormatConverter(converter.GetAddressOf());
    if (SUCCEEDED(result)) result = converter->Initialize(frame.Get(),GUID_WICPixelFormat32bppPBGRA,WICBitmapDitherTypeNone,nullptr,0,WICBitmapPaletteTypeCustom);
    if (SUCCEEDED(result)) result = target_->CreateBitmapFromWicBitmap(converter.Get(),background_.GetAddressOf());
    if (FAILED(result)) throw std::runtime_error("无法加载安装界面资源。");
  }

  ComPtr<ID2D1SolidColorBrush> Brush(D2D1_COLOR_F color) {
    ComPtr<ID2D1SolidColorBrush> brush;
    target_->CreateSolidColorBrush(color,brush.GetAddressOf());
    return brush;
  }
  void Rounded(D2D1_RECT_F rect, float radius, D2D1_COLOR_F color, bool stroke = false, float thickness = 1) {
    auto brush = Brush(color);
    if (stroke) target_->DrawRoundedRectangle(D2D1::RoundedRect(rect,radius,radius),brush.Get(),thickness);
    else target_->FillRoundedRectangle(D2D1::RoundedRect(rect,radius,radius),brush.Get());
  }
  void Line(float x1,float y1,float x2,float y2,D2D1_COLOR_F color,float thickness = 1.5f) {
    auto brush=Brush(color);
    target_->DrawLine(D2D1::Point2F(x1,y1),D2D1::Point2F(x2,y2),brush.Get(),thickness);
  }
  void Text(const std::wstring& value,float size,D2D1_RECT_F rect,D2D1_COLOR_F color = Ink,
            DWRITE_FONT_WEIGHT weight = DWRITE_FONT_WEIGHT_NORMAL,bool centered = false) {
    ComPtr<IDWriteTextFormat> format;
    if (FAILED(write_->CreateTextFormat(value == L"Octopus" ? L"Segoe UI" : fontFamily_.c_str(),nullptr,weight,DWRITE_FONT_STYLE_NORMAL,
        DWRITE_FONT_STRETCH_NORMAL,size,L"zh-CN",format.GetAddressOf()))) return;
    format->SetParagraphAlignment(DWRITE_PARAGRAPH_ALIGNMENT_CENTER);
    format->SetTextAlignment(centered ? DWRITE_TEXT_ALIGNMENT_CENTER : DWRITE_TEXT_ALIGNMENT_LEADING);
    format->SetWordWrapping(DWRITE_WORD_WRAPPING_WRAP);
    auto brush=Brush(color);
    target_->DrawText(value.c_str(),static_cast<UINT32>(value.size()),format.Get(),rect,brush.Get(),D2D1_DRAW_TEXT_OPTIONS_CLIP);
  }

  void Brand() {
    ComPtr<ID2D1PathGeometry> geometry;
    ComPtr<ID2D1GeometrySink> sink;
    factory_->CreatePathGeometry(geometry.GetAddressOf());
    geometry->Open(sink.GetAddressOf());
    const float x = 34, y = 28, s = .30f;
    auto p=[&](float a,float b){return D2D1::Point2F(x+a*s,y+b*s);};
    sink->BeginFigure(p(30,80),D2D1_FIGURE_BEGIN_HOLLOW);
    sink->AddBezier(D2D1::BezierSegment(p(15,65),p(15,30),p(32,20)));
    sink->AddBezier(D2D1::BezierSegment(p(43,10),p(65,13),p(76,25)));
    sink->AddBezier(D2D1::BezierSegment(p(92,44),p(84,65),p(70,80)));
    sink->AddBezier(D2D1::BezierSegment(p(70,88),p(74,90),p(82,90)));
    sink->EndFigure(D2D1_FIGURE_END_OPEN);
    for (auto pair : {std::pair<float,float>{30,20},{43,38},{57,62}}) {
      sink->BeginFigure(p(pair.first,77),D2D1_FIGURE_BEGIN_HOLLOW);
      sink->AddBezier(D2D1::BezierSegment(p(pair.first,86),p(pair.first,90),p(pair.second,90)));
      sink->EndFigure(D2D1_FIGURE_END_OPEN);
    }
    sink->Close();
    auto brush=Brush(Green);
    target_->DrawGeometry(geometry.Get(),brush.Get(),2.2f);
    Text(L"Octopus",25,D2D1::RectF(75,25,198,62),Ink,DWRITE_FONT_WEIGHT_SEMI_BOLD);
    Text(L"本地 AI 网关",14,D2D1::RectF(207,29,350,60),Muted);
  }

  void Paint() {
    PAINTSTRUCT paint{};
    HDC dc=BeginPaint(window_,&paint);
    RECT bounds{};
    GetClientRect(window_,&bounds);
    target_->BindDC(dc,&bounds);
    target_->BeginDraw();
    target_->SetTransform(D2D1::Matrix3x2F::Scale(scale_,scale_));
    target_->Clear(White);
    target_->DrawBitmap(background_.Get(),D2D1::RectF(0,0,Width,Height));
    Brand();
    if (page_ == Page::Welcome) {
      if (!advanced_) {
        Text(L"连接模型",60,D2D1::RectF(72,119,660,192),Ink,DWRITE_FONT_WEIGHT_BOLD);
        Text(L"释放可能",60,D2D1::RectF(72,192,660,265),Ink,DWRITE_FONT_WEIGHT_BOLD);
        Text(L"一个入口，连接你的 AI 世界",20,D2D1::RectF(75,278,735,311),Muted);
      } else {
        Text(L"安装选项",35,D2D1::RectF(130,112,710,169),Ink,DWRITE_FONT_WEIGHT_SEMI_BOLD);
        Text(L"选择适合你的安装范围",16,D2D1::RectF(130,170,710,198),Muted);
        Text(options_.allUsers ? L"所有用户均可使用，安装时需要 Windows 管理员授权。" : L"为当前账户安装，配置和账号数据保存在个人目录。",
             14,D2D1::RectF(130,277,710,323),Muted);
      }
      Rounded(D2D1::RectF(130,354,710,405),10,Field);
      Rounded(D2D1::RectF(130.5f,354.5f,709.5f,404.5f),10,Outline,true);
      Text(L"安装位置",17,D2D1::RectF(149,356,242,403),Ink,DWRITE_FONT_WEIGHT_SEMI_BOLD);
    } else DrawStatus();
    target_->EndDraw();
    EndPaint(window_,&paint);
    if (!painted_) {
      painted_=true;
      if (!args_.smokeReport.empty()) {
        std::ofstream out(std::filesystem::path(args_.smokeReport),std::ios::binary);
        out << "{\"ready_ms\":" << (GetTickCount64()-started_) << ",\"scale\":" << scale_
            << ",\"width\":" << bounds.right << ",\"height\":" << bounds.bottom
            << ",\"payload_bytes\":" << kPayloadBytes << ",\"background_loaded\":true,\"visible\":"
            << (IsWindowVisible(window_) ? "true" : "false") << "}";
        SetTimer(window_,2,900,nullptr);
      }
    }
  }

  void DrawStatus() {
    const bool failed=page_ == Page::Failed, complete=page_ == Page::Complete;
    const auto accent = failed ? Color(0xc65c45) : Green;
    auto tint=accent; tint.a=.10f;
    auto brush=Brush(tint);
    target_->FillEllipse(D2D1::Ellipse(D2D1::Point2F(420,188),38,38),brush.Get());
    if (failed) { Line(409,177,431,199,accent,3); Line(431,177,409,199,accent,3); }
    else if (complete) { Line(405,189,416,200,accent,3); Line(416,200,437,177,accent,3); }
    else {
      const float t=static_cast<float>(GetTickCount64()%1600)/1600*6.2831853f;
      for(int i=0;i<10;++i) {
        auto dot=accent; dot.a=.15f+.085f*i;
        auto b=Brush(dot);
        const float a=t+i*.48f;
        target_->FillEllipse(D2D1::Ellipse(D2D1::Point2F(420+18*std::cos(a),188+18*std::sin(a)),2.4f,2.4f),b.Get());
      }
    }
    Text(complete ? L"安装完成" : failed ? L"安装没有完成" : preparing_ ? L"正在准备安装" : L"正在安装 Octopus",
         29,D2D1::RectF(90,241,750,290),Ink,DWRITE_FONT_WEIGHT_SEMI_BOLD,true);
    Text(complete ? L"Octopus 已准备就绪，可以开始使用了。" : message_,
         15,D2D1::RectF(108,297,732,356),Muted,DWRITE_FONT_WEIGHT_NORMAL,true);
    if (!failed) {
      Rounded(D2D1::RectF(170,375,670,381),3,Color(0xe8e9e8));
      if (complete) Rounded(D2D1::RectF(170,375,670,381),3,Green);
      else if (fraction_ >= 0) Rounded(D2D1::RectF(170,375,170+500*std::clamp(fraction_,0.f,1.f),381),3,Green);
      else {
        const float t=static_cast<float>(GetTickCount64()%1800)/1800;
        const float x=170+400*(.5f-.5f*std::cos(t*6.2831853f));
        Rounded(D2D1::RectF(x,375,x+100,381),3,Green);
      }
    }
  }

  void DrawControl(HWND control,HDC dc,RECT rect,bool pressed,bool focused) {
    const int id=GetDlgCtrlID(control);
    target_->BindDC(dc,&rect);
    target_->BeginDraw();
    target_->SetTransform(D2D1::Matrix3x2F::Scale(scale_,scale_));
    const float w=(rect.right-rect.left)/scale_, h=(rect.bottom-rect.top)/scale_;
    target_->Clear(id == Browse ? Field : White);
    if (id != Browse) {
      RECT location{};
      GetWindowRect(control, &location);
      MapWindowPoints(HWND_DESKTOP, window_, reinterpret_cast<POINT*>(&location), 2);
      const float x = location.left / scale_, y = location.top / scale_;
      const auto size = background_->GetPixelSize();
      target_->DrawBitmap(background_.Get(), D2D1::RectF(0,0,w,h), 1, D2D1_BITMAP_INTERPOLATION_MODE_LINEAR,
          D2D1::RectF(x/Width*size.width, y/Height*size.height, (x+w)/Width*size.width, (y+h)/Height*size.height));
    }
    const bool enabled=IsWindowEnabled(control)!=FALSE;
    wchar_t caption[128]{};
    GetWindowTextW(control,caption,128);
    if (id == Primary) {
      Rounded(D2D1::RectF(0,0,w,h),10,!enabled ? Color(0x93af9d) : pressed ? Color(0x3e6e51) : Green);
      Text(caption,21,D2D1::RectF(5,0,w-5,h),White,DWRITE_FONT_WEIGHT_SEMI_BOLD,true);
      if(focused) Rounded(D2D1::RectF(3,3,w-3,h-3),8,Color(0xffffff,.72f),true,1.5f);
    } else if(id == Minimize || id == Close) {
      if(pressed || focused) Rounded(D2D1::RectF(1,1,w-1,h-1),7,Color(0xffffff,.5f));
      if(id == Minimize) Line(w/2-6,h/2+1,w/2+6,h/2+1,Muted,1.6f);
      else { Line(w/2-6,h/2-6,w/2+6,h/2+6,Muted,1.6f); Line(w/2+6,h/2-6,w/2-6,h/2+6,Muted,1.6f); }
    } else if(id == Browse) {
      if(focused || pressed) Rounded(D2D1::RectF(3,3,w-3,h-3),7,Color(0xe7ece8));
      Rounded(D2D1::RectF(10,14,31,29),1.5f,Muted,true,1.6f);
      Line(10,14,10,10,Muted,1.6f); Line(10,10,18,10,Muted,1.6f); Line(18,10,22,14,Muted,1.6f);
    } else if(id == Shortcut) {
      const bool checked=SendMessageW(control,BM_GETCHECK,0,0)==BST_CHECKED;
      Rounded(D2D1::RectF(1,6,20,25),4,checked ? Green : White);
      if(checked) { Line(5,15,9,20,White,2); Line(9,20,16,11,White,2); }
      else Rounded(D2D1::RectF(1,6,20,25),4,Outline,true);
      Text(caption,15,D2D1::RectF(30,0,w,h),Ink);
      if(focused) Rounded(D2D1::RectF(.5f,.5f,w-.5f,h-.5f),6,Green,true);
    } else if(id == CurrentUser || id == AllUsers) {
      const bool checked=SendMessageW(control,BM_GETCHECK,0,0)==BST_CHECKED;
      Rounded(D2D1::RectF(1,1,w-1,h-1),9,checked ? Color(0xeef5f0) : Field);
      Rounded(D2D1::RectF(1,1,w-1,h-1),9,checked || focused ? Green : Outline,true);
      auto b=Brush(checked ? Green : Muted);
      target_->DrawEllipse(D2D1::Ellipse(D2D1::Point2F(24,h/2),7,7),b.Get(),1.5f);
      if(checked) target_->FillEllipse(D2D1::Ellipse(D2D1::Point2F(24,h/2),3.5f,3.5f),b.Get());
      Text(caption,16,D2D1::RectF(44,0,w-8,h),Ink);
    } else {
      Text(caption,15,D2D1::RectF(0,0,w-18,h),Muted,DWRITE_FONT_WEIGHT_NORMAL,id == Advanced);
      if(id == Advanced) { Line(w-9,h/2-4,w-5,h/2,Muted); Line(w-5,h/2,w-9,h/2+4,Muted); }
      if(focused) Rounded(D2D1::RectF(.5f,.5f,w-.5f,h-.5f),5,Green,true);
    }
    target_->EndDraw();
  }

  void Act(int id) {
    if(id == Minimize) { ShowWindow(window_,SW_MINIMIZE); return; }
    if(id == Close) { RequestClose(); return; }
    if(page_ == Page::Installing) return;
    if(id == Advanced) {
      if(page_ == Page::Complete) { RequestClose(); return; }
      if(page_ == Page::Failed) page_=Page::Welcome;
      else advanced_=!advanced_;
      Refresh(); return;
    }
    if(id == Shortcut) { options_.desktopShortcut=SendDlgItemMessageW(window_,Shortcut,BM_GETCHECK,0,0)==BST_CHECKED; InvalidateRect(GetDlgItem(window_,Shortcut),nullptr,FALSE); return; }
    if(id == CurrentUser || id == AllUsers) {
      options_.allUsers=id == AllUsers;
      CheckRadioButton(window_,CurrentUser,AllUsers,id);
      try { options_.directory=setup::DefaultDirectory(options_.allUsers); SetWindowTextW(path_,options_.directory.c_str()); }
      catch(const std::exception& e) { Alert(setup::Wide(e.what())); }
      Refresh(); return;
    }
    if(id == Browse) { SelectDirectory(); return; }
    if(id == LogLink) { if(!logPath_.empty()) ShellExecuteW(window_,L"open",logPath_.c_str(),nullptr,nullptr,SW_SHOWNORMAL); return; }
    if(id != Primary) return;
    if(page_ == Page::Complete) {
      if(args_.preview) { RequestClose(); return; }
      const auto executable=std::filesystem::path(options_.directory)/kAppExecutable;
      const auto result=reinterpret_cast<INT_PTR>(ShellExecuteW(window_,L"open",executable.c_str(),nullptr,options_.directory.c_str(),SW_SHOWNORMAL));
      if(result<=32) Alert(L"无法启动 Octopus，请从开始菜单重试。");
      else RequestClose();
      return;
    }
    if(args_.preview) { page_=Page::Complete; message_.clear(); Refresh(); return; }
    StartInstall();
  }

  void Alert(const std::wstring& text) { MessageBoxW(window_,text.c_str(),L"Octopus 安装",MB_OK|MB_ICONINFORMATION); }

  void SelectDirectory() {
    ComPtr<IFileOpenDialog> dialog;
    if(FAILED(CoCreateInstance(CLSID_FileOpenDialog,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(dialog.GetAddressOf())))) return;
    DWORD flags=0;
    dialog->GetOptions(&flags);
    dialog->SetOptions(flags|FOS_PICKFOLDERS|FOS_FORCEFILESYSTEM|FOS_PATHMUSTEXIST);
    dialog->SetTitle(L"选择 Octopus 安装位置");
    if(FAILED(dialog->Show(window_))) return;
    ComPtr<IShellItem> item;
    if(FAILED(dialog->GetResult(item.GetAddressOf()))) return;
    PWSTR selected=nullptr;
    if(SUCCEEDED(item->GetDisplayName(SIGDN_FILESYSPATH,&selected))) {
      auto folder=std::filesystem::path(selected);
      CoTaskMemFree(selected);
      if(_wcsicmp(folder.filename().c_str(),kProductDirectory)!=0 && !std::filesystem::exists(folder/kAppExecutable)) folder/=kProductDirectory;
      SetWindowTextW(path_,folder.c_str());
    }
  }

  void StartInstall() {
    wchar_t directory[260]{};
    GetWindowTextW(path_,directory,260);
    try { options_.directory=setup::ValidateDirectory(directory); }
    catch(const std::exception& e) { Alert(setup::Wide(e.what())); return; }
    if(setup::AppIsRunning() && MessageBoxW(window_,L"安装会关闭正在运行的 Octopus 并更新程序。请先完成正在进行的操作。\n\n继续安装？",
        L"更新 Octopus",MB_YESNO|MB_ICONINFORMATION|MB_DEFBUTTON2)!=IDYES) return;
    if(worker_.joinable()) worker_.join();
    page_=Page::Installing;
    preparing_=true;
    fraction_=0;
    message_=L"正在准备安装文件";
    logPath_.clear();
    Refresh();
    SetTimer(window_,1,30,nullptr);
    worker_=std::thread([this, options=options_] {
      setup::Install(instance_,window_,options,[this](setup::Update update) {
        auto payload=new setup::Update(std::move(update));
        if(!PostMessageW(window_,UpdateMessage,0,reinterpret_cast<LPARAM>(payload))) delete payload;
      });
    });
  }

  void Receive(const setup::Update& update) {
    message_=update.message;
    logPath_=update.logPath;
    fraction_=update.fraction;
    preparing_=update.phase == setup::Phase::Preparing;
    if(update.phase == setup::Phase::Complete || update.phase == setup::Phase::Failed) {
      page_=update.phase == setup::Phase::Complete ? Page::Complete : Page::Failed;
      KillTimer(window_,1);
      if(worker_.joinable()) worker_.join();
      Refresh();
      SetFocus(GetDlgItem(window_,Primary));
    } else InvalidateRect(window_,nullptr,FALSE);
  }

  void RequestClose() {
    if(page_ == Page::Installing && !args_.preview) { Alert(L"安装正在进行，请等待完成。你可以先将窗口最小化。"); return; }
    DestroyWindow(window_);
  }

  HINSTANCE instance_;
  Arguments args_;
  HWND window_=nullptr, path_=nullptr;
  HFONT editFont_=nullptr;
  HBRUSH fieldBrush_=nullptr;
  float scale_=1, fraction_=-1;
  bool advanced_=false, preparing_=false, painted_=false;
  Page page_=Page::Welcome;
  ULONGLONG started_;
  setup::InstallOptions options_;
  std::wstring message_, logPath_, fontFamily_=L"Microsoft YaHei UI";
  std::thread worker_;
  ComPtr<ID2D1Factory> factory_;
  ComPtr<IDWriteFactory> write_;
  ComPtr<IWICImagingFactory> wic_;
  ComPtr<ID2D1DCRenderTarget> target_;
  ComPtr<ID2D1Bitmap> background_;
};
}  // namespace

int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,PWSTR,int show) {
  SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
  const HRESULT initialized=CoInitializeEx(nullptr,COINIT_APARTMENTTHREADED);
  int result=1;
  try {
    Arguments args;
    args.preview = kPayloadBytes == 0;
    int count=0;
    LPWSTR* values=CommandLineToArgvW(GetCommandLineW(),&count);
    for(int i=1;i<count;++i) {
      const std::wstring arg=values[i];
      if(arg == L"--preview") args.preview=true;
      else if(arg == L"--preview-state" && i+1<count) { args.preview=true; args.previewState=values[++i]; }
      else if(arg == L"--preview-scale" && i+1<count) { args.preview=true; args.previewScale=std::clamp(std::stof(values[++i]),1.f,2.f); }
      else if(arg == L"--smoke-test" && i+1<count) { args.preview=true; args.smokeReport=values[++i]; }
      else if(arg == L"--self-test" && i+1<count) args.selfTest=values[++i];
    }
    LocalFree(values);
    if(!args.selfTest.empty()) result=setup::SelfTest(instance,args.selfTest) ? 0 : 1;
    else { Window window(instance,std::move(args)); result=window.Run(show); }
  } catch(const std::exception& error) {
    MessageBoxW(nullptr,setup::Wide(error.what()).c_str(),L"Octopus 安装",MB_OK|MB_ICONERROR);
  }
  if(SUCCEEDED(initialized)) CoUninitialize();
  return result;
}
