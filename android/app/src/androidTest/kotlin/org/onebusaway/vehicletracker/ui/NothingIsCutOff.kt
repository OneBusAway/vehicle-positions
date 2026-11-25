package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher.Companion.keyIsDefined
import androidx.compose.ui.test.SemanticsNodeInteractionsProvider
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.text.TextLayoutResult
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue

/** No text on screen is cut short or pushed off its side. */
internal fun SemanticsNodeInteractionsProvider.assertNothingIsCutOff() {
    val root = onRoot().fetchSemanticsNode().boundsInRoot
    val texts = onAllNodes(keyIsDefined(SemanticsActions.GetTextLayoutResult), useUnmergedTree = true).fetchSemanticsNodes()
    assertTrue(texts.isNotEmpty())
    for (node in texts) {
        val layouts = mutableListOf<TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action?.invoke(layouts)
        for (layout in layouts) {
            val words = layout.layoutInput.text.text
            val lines = 0 until layout.lineCount
            assertFalse("\"$words\" is cut short", layout.didOverflowHeight || lines.any(layout::isLineEllipsized))
            assertTrue("\"$words\" is wider than its place", lines.all { layout.getLineRight(it) <= layout.size.width + 0.5f })
            assertTrue("\"$words\" runs off the side", node.boundsInRoot.left >= 0f && node.boundsInRoot.right <= root.right + 0.5f)
        }
    }
}
