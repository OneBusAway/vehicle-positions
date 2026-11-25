package org.onebusaway.vehicletracker.ui.components

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

// A group's rows are joined: round on the group's outside, tight where two rows meet.
internal val GroupOuterCorner = 20.dp
private val InnerCorner = 4.dp
private val RowGap = 3.dp

/** Where a row sits in its group, which decides its corners. */
internal enum class GroupPlace { ONLY, FIRST, MIDDLE, LAST }

internal fun groupPlace(index: Int, size: Int): GroupPlace = when {
    size == 1 -> GroupPlace.ONLY
    index == 0 -> GroupPlace.FIRST
    index == size - 1 -> GroupPlace.LAST
    else -> GroupPlace.MIDDLE
}

private val GroupPlace.shape: RoundedCornerShape
    get() = when (this) {
        GroupPlace.ONLY -> RoundedCornerShape(GroupOuterCorner)
        GroupPlace.FIRST -> RoundedCornerShape(GroupOuterCorner, GroupOuterCorner, InnerCorner, InnerCorner)
        GroupPlace.MIDDLE -> RoundedCornerShape(InnerCorner)
        GroupPlace.LAST -> RoundedCornerShape(InnerCorner, InnerCorner, GroupOuterCorner, GroupOuterCorner)
    }

/** Spaces a row from the one above it in its group and rounds it for its [place]. */
internal fun Modifier.groupRow(place: GroupPlace): Modifier {
    val first = place == GroupPlace.ONLY || place == GroupPlace.FIRST
    return padding(top = if (first) 0.dp else RowGap).clip(place.shape)
}

/** The title over a group of rows. */
@Composable
internal fun GroupTitle(@StringRes title: Int) {
    Text(
        text = stringResource(title),
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier
            .padding(start = 4.dp, top = 24.dp, bottom = 8.dp)
            .semantics { heading() },
    )
}
