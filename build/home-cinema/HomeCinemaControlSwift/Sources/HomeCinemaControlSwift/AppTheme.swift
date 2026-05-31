import SwiftUI

/// Liquid Glass palette — основано на mockup'е «Home Cinema · Liquid Glass»:
/// тёплая cream-бумага (#faf9f5) в светлой теме, глубокий film-black (#0e0a08)
/// в тёмной, янтарь #FF8A3D — единственный «горячий» акцент. Деликатные
/// burgundy-уровни (#5c2b2e, #2a1215) поддерживают тёплую warmth без
/// расщепления палитры.
enum AppTheme: String, CaseIterable, Identifiable {
    case light
    case dark

    var id: String { rawValue }

    var title: String {
        switch self {
        case .light: return "Light"
        case .dark:  return "Dark"
        }
    }

    var colorScheme: ColorScheme {
        switch self {
        case .light: return .light
        case .dark:  return .dark
        }
    }

    // MARK: – Accent

    /// Янтарный #FF8A3D — единственный «hot» цвет. Используется только для
    /// living-индикаторов и primary CTA, иначе теряет силу.
    var accent: Color { Color(red: 1.00, green: 0.541, blue: 0.239) } // #FF8A3D
    var accentSoft: Color { Color(red: 1.00, green: 0.706, blue: 0.329) } // #FFB454

    var accentForeground: Color { Color(red: 0.055, green: 0.039, blue: 0.031) } // ink on amber

    var success: Color {
        switch self {
        case .light: return Color(red: 0.18, green: 0.55, blue: 0.36)
        case .dark:  return Color(red: 0.42, green: 0.84, blue: 0.60)
        }
    }

    var danger: Color {
        switch self {
        case .light: return Color(red: 0.72, green: 0.20, blue: 0.18)
        case .dark:  return Color(red: 1.00, green: 0.502, blue: 0.502) // #ff8a80
        }
    }

    // MARK: – Ground (page background)

    var paper: Color {
        switch self {
        case .light: return Color(red: 0.980, green: 0.976, blue: 0.961) // #faf9f5
        case .dark:  return Color(red: 0.055, green: 0.039, blue: 0.031) // #0e0a08
        }
    }

    /// Глубокий film-фон — самый дальний слой под panel'ями.
    var paperDeep: Color {
        switch self {
        case .light: return Color(red: 0.945, green: 0.934, blue: 0.912)
        case .dark:  return Color(red: 0.030, green: 0.024, blue: 0.020) // #08060a
        }
    }

    /// Warm wash под акцентом hero-блока (radial gradient origin).
    var ambientWarm: Color {
        switch self {
        case .light: return Color(red: 1.00, green: 0.541, blue: 0.239).opacity(0.16)
        case .dark:  return Color(red: 1.00, green: 0.541, blue: 0.239).opacity(0.22)
        }
    }

    /// Cool counter-spot для глубины.
    var ambientCool: Color {
        switch self {
        case .light: return Color(red: 0.36, green: 0.17, blue: 0.18).opacity(0.10) // #5c2b2e
        case .dark:  return Color(red: 0.36, green: 0.17, blue: 0.18).opacity(0.28)
        }
    }

    // MARK: – Glass surfaces (поверх Material)

    /// Tint поверх .regularMaterial — добавляет тёплый колорит к стеклу.
    var glassTint: Color {
        switch self {
        case .light: return Color.white.opacity(0.18)
        case .dark:  return Color(red: 0.10, green: 0.06, blue: 0.04).opacity(0.30)
        }
    }

    /// Highlight (inner stroke) — имитирует «преломление» света на верхней
    /// кромке liquid glass.
    var glassHighlight: Color {
        switch self {
        case .light: return Color.white.opacity(0.55)
        case .dark:  return Color.white.opacity(0.10)
        }
    }

    /// Outer border (внешняя кромка стекла).
    var glassEdge: Color {
        switch self {
        case .light: return Color.black.opacity(0.06)
        case .dark:  return Color.white.opacity(0.07)
        }
    }

    var glassEdgeStrong: Color {
        switch self {
        case .light: return Color.black.opacity(0.12)
        case .dark:  return Color.white.opacity(0.14)
        }
    }

    /// Поверхность tiles (Recently Watched), чуть плотнее основного стекла.
    var tileSurface: Color {
        switch self {
        case .light: return Color.white.opacity(0.52)
        case .dark:  return Color(red: 0.10, green: 0.08, blue: 0.06).opacity(0.55)
        }
    }

    // MARK: – Hero (deepest spotlight)

    /// Hero — почти-чёрный warm-tinted spotlight, чтобы амбер-accent читался
    /// как луч кинопроектора, а не как warning.
    var heroTop: Color {
        switch self {
        case .light: return Color(red: 0.075, green: 0.055, blue: 0.040) // #1a0f0a
        case .dark:  return Color(red: 0.075, green: 0.055, blue: 0.040)
        }
    }
    var heroBottom: Color {
        switch self {
        case .light: return Color(red: 0.040, green: 0.027, blue: 0.020)
        case .dark:  return Color(red: 0.030, green: 0.020, blue: 0.016)
        }
    }
    var heroPrimaryText: Color { Color(red: 0.98, green: 0.972, blue: 0.957) }
    var heroSecondaryText: Color { Color.white.opacity(0.68) }
    var heroSubtleText: Color { Color.white.opacity(0.42) }

    // MARK: – Text on paper

    var primaryText: Color {
        switch self {
        case .light: return Color(red: 0.075, green: 0.055, blue: 0.040)
        case .dark:  return Color(red: 0.96, green: 0.945, blue: 0.918)
        }
    }
    var secondaryText: Color {
        switch self {
        case .light: return Color(red: 0.32, green: 0.28, blue: 0.24)
        case .dark:  return Color.white.opacity(0.72)
        }
    }
    var subtleText: Color {
        switch self {
        case .light: return Color(red: 0.50, green: 0.46, blue: 0.42)
        case .dark:  return Color.white.opacity(0.48)
        }
    }

    // MARK: – Shadows

    /// Outer shadow для floating glass-элементов. Liquid Glass отбрасывает
    /// мягкую тёплую тень, не чисто чёрную.
    var shadow: Color {
        switch self {
        case .light: return Color(red: 0.36, green: 0.17, blue: 0.18).opacity(0.10)
        case .dark:  return Color.black.opacity(0.55)
        }
    }

    // MARK: – Window chrome

    var windowButtonFill: Color {
        switch self {
        case .light: return Color.black.opacity(0.06)
        case .dark:  return Color.white.opacity(0.10)
        }
    }
    var windowButtonGlyph: Color {
        switch self {
        case .light: return Color(red: 0.10, green: 0.07, blue: 0.05)
        case .dark:  return Color.white.opacity(0.86)
        }
    }
}

// MARK: - Typography

/// Geist (Vercel) — основной шрифт mockup'а. SwiftUI Font.custom тихо
/// деградирует до системного, если Geist не установлен в систему, — UX
/// остаётся приличный, но рекомендуем установить пакет Geist (https://vercel.com/font).
enum Typography {
    /// Display heading — Geist heavy 38pt.
    static func display(_ size: CGFloat = 38) -> Font {
        Font.custom("Geist", size: size).weight(.heavy)
    }
    static func h1(_ size: CGFloat = 24) -> Font {
        Font.custom("Geist", size: size).weight(.bold)
    }
    static func h2(_ size: CGFloat = 16) -> Font {
        Font.custom("Geist", size: size).weight(.semibold)
    }
    static func body(_ size: CGFloat = 13) -> Font {
        Font.custom("Geist", size: size).weight(.medium)
    }
    static func small(_ size: CGFloat = 11) -> Font {
        Font.custom("Geist", size: size).weight(.semibold)
    }
    /// SMALL-CAPS-style label — tracking настраивается на месте.
    static func label(_ size: CGFloat = 10) -> Font {
        Font.custom("Geist", size: size).weight(.heavy)
    }
    static func mono(_ size: CGFloat = 12) -> Font {
        Font.custom("Geist Mono", size: size).weight(.semibold)
    }
    static func timecode(_ size: CGFloat = 14) -> Font {
        Font.custom("Geist Mono", size: size).weight(.bold)
    }
}
