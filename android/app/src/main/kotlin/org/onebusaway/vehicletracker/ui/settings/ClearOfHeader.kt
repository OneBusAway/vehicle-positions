package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.toRect
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.node.ModifierNodeElement
import androidx.compose.ui.node.requireLayoutCoordinates
import androidx.compose.ui.platform.InspectorInfo
import androidx.compose.ui.relocation.BringIntoViewModifierNode
import androidx.compose.ui.relocation.bringIntoView
import androidx.compose.ui.unit.toSize

/**
 * What is scrolled to in here, as keyboard focus moves to it, comes out from under the folded header, which is drawn over
 * the top of the page: it asks for the room the header takes as well as its own.
 */
internal fun Modifier.clearOfHeader(fold: HeaderFold): Modifier = this then ClearOfHeaderElement(fold)

private data class ClearOfHeaderElement(val fold: HeaderFold) : ModifierNodeElement<ClearOfHeaderNode>() {
    override fun create() = ClearOfHeaderNode(fold)

    override fun update(node: ClearOfHeaderNode) {
        node.fold = fold
    }

    override fun InspectorInfo.inspectableProperties() {
        name = "clearOfHeader"
    }
}

private class ClearOfHeaderNode(var fold: HeaderFold) : Modifier.Node(), BringIntoViewModifierNode {
    override suspend fun bringIntoView(childCoordinates: LayoutCoordinates, boundsProvider: () -> Rect?) {
        bringIntoView {
            if (!isAttached || !childCoordinates.isAttached) return@bringIntoView null
            val bounds = boundsProvider() ?: childCoordinates.size.toSize().toRect()
            val inHere = bounds.translate(requireLayoutCoordinates().localPositionOf(childCoordinates, Offset.Zero))
            inHere.copy(top = inHere.top - fold.folded)
        }
    }
}
