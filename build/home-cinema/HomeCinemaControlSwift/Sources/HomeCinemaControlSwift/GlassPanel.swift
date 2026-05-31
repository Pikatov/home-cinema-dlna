import SwiftUI

/// Liquid Glass surface — эмулирует Apple iOS 26 / macOS 16 `glassEffect()`
/// API на macOS 14+, где этот API ещё недоступен. Состав слоёв (снизу вверх):
///
///   1. `.regularMaterial` — фактическое стекло (backdrop blur + saturation)
///   2. `glassTint` — тёплый цветной wash, отличающий это стекло от системного
///   3. inner gradient highlight — имитирует «преломление» света на верхней
///      кромке (light leak)
///   4. outer border (`glassEdge`)
///
/// + soft warm shadow для floating ощущения.
///
/// Использование: `view.glassPanel(theme: theme)` или `glassPanel(theme:cornerRadius:)`.
struct GlassPanelModifier: ViewModifier {
    let theme: AppTheme
    var cornerRadius: CGFloat = 20
    var elevation: CGFloat = 1
    var prominent: Bool = false

    func body(content: Content) -> some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        content
            .background(material, in: shape)
            .overlay(shape.fill(theme.glassTint).allowsHitTesting(false))
            .overlay(
                shape
                    .strokeBorder(
                        LinearGradient(
                            colors: [theme.glassHighlight, Color.clear],
                            startPoint: .top,
                            endPoint: .center
                        ),
                        lineWidth: 1
                    )
                    .allowsHitTesting(false)
            )
            .overlay(
                shape
                    .stroke(theme.glassEdge, lineWidth: 1)
                    .allowsHitTesting(false)
            )
            .clipShape(shape)
            .shadow(color: theme.shadow.opacity(0.65 * elevation),
                    radius: 18 * elevation, x: 0, y: 10 * elevation)
    }

    private var material: Material {
        prominent ? .thickMaterial : .regularMaterial
    }
}

extension View {
    /// Liquid Glass поверхность. `prominent=true` использует .thickMaterial
    /// для большего contrast (например, hero-блок).
    func glassPanel(theme: AppTheme,
                    cornerRadius: CGFloat = 20,
                    elevation: CGFloat = 1,
                    prominent: Bool = false) -> some View {
        modifier(GlassPanelModifier(theme: theme,
                                    cornerRadius: cornerRadius,
                                    elevation: elevation,
                                    prominent: prominent))
    }

    /// Capsule-вариант — для chip'ов и pill-индикаторов.
    func glassPill(theme: AppTheme) -> some View {
        let shape = Capsule(style: .continuous)
        return self
            .background(.regularMaterial, in: shape)
            .overlay(shape.fill(theme.glassTint).allowsHitTesting(false))
            .overlay(
                shape
                    .strokeBorder(theme.glassHighlight, lineWidth: 0.8)
                    .allowsHitTesting(false)
            )
            .overlay(
                shape
                    .stroke(theme.glassEdge, lineWidth: 0.6)
                    .allowsHitTesting(false)
            )
            .clipShape(shape)
    }
}
