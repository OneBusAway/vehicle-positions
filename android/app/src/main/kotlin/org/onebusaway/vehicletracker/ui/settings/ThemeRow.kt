package org.onebusaway.vehicletracker.ui.settings

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.ui.components.ChoiceRow
import org.onebusaway.vehicletracker.ui.components.GroupPlace
import org.onebusaway.vehicletracker.ui.components.QuietButton

/** The app's theme, and the list a tap on it opens. */
@Composable
internal fun ThemeRow(theme: ThemeChoice, place: GroupPlace, onChoose: (ThemeChoice) -> Unit) {
    var listOpen by rememberSaveable { mutableStateOf(false) }
    SettingsRow(
        icon = R.drawable.ic_contrast,
        title = stringResource(R.string.settings_theme),
        detail = stringResource(nameOf(theme)),
        place = place,
        opens = true,
        onClick = { listOpen = true },
    )
    if (listOpen) {
        ThemeList(
            theme = theme,
            onChoose = {
                listOpen = false
                onChoose(it)
            },
            onDismiss = { listOpen = false },
        )
    }
}

@StringRes
private fun nameOf(theme: ThemeChoice): Int = when (theme) {
    ThemeChoice.SYSTEM -> R.string.theme_system
    ThemeChoice.LIGHT -> R.string.theme_light
    ThemeChoice.DARK -> R.string.theme_dark
}

/** The app's themes. A tap on one chooses it and closes the list. */
@Composable
private fun ThemeList(theme: ThemeChoice, onChoose: (ThemeChoice) -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(painterResource(R.drawable.ic_contrast), contentDescription = null) },
        iconContentColor = MaterialTheme.colorScheme.onSurfaceVariant,
        title = { Text(stringResource(R.string.settings_theme)) },
        text = {
            Column(Modifier.selectableGroup().verticalScroll(rememberScrollState())) {
                for (choice in ThemeChoice.entries) {
                    ChoiceRow(
                        title = AnnotatedString(stringResource(nameOf(choice))),
                        detail = if (choice == ThemeChoice.SYSTEM) stringResource(R.string.theme_system_detail) else null,
                        selected = choice == theme,
                        onClick = { onChoose(choice) },
                    )
                }
            }
        },
        confirmButton = { QuietButton(stringResource(R.string.language_list_cancel), onClick = onDismiss) },
    )
}
