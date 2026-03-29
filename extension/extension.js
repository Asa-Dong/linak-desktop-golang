import { Extension } from 'resource:///org/gnome/shell/extensions/extension.js';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as QuickSettings from 'resource:///org/gnome/shell/ui/quickSettings.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';
import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GObject from 'gi://GObject';
import St from 'gi://St';

const DeskInterface = `
<node>
  <interface name="org.linak.Desk">
    <method name="Up" />
    <method name="Down" />
    <method name="Stop" />
    <method name="MoveToSit" />
    <method name="MoveToStand" />
    <method name="RefreshPosition" />
    <property name="Position" type="d" access="read" />
  </interface>
</node>
`;

const DeskProxy = Gio.DBusProxy.makeProxyWrapper(DeskInterface);

const DeskToggle = GObject.registerClass(
class DeskToggle extends QuickSettings.QuickMenuToggle {
    _init(extension) {
        super._init({
            title: 'Desk Control',
            iconName: 'input-gaming-symbolic',
            toggleMode: false,
        });

        this._extension = extension;
        this._proxy = new DeskProxy(Gio.DBus.session, 'org.linak.Desk', '/org/linak/Desk');

        this.menu.setHeader('input-gaming-symbolic', 'Desk Control', '');

        const createButton = (iconName, labelText, callback) => {
            const btn = new St.Button({
                style_class: 'button',
                x_expand: true,
                can_focus: true,
            });
            btn.set_style('padding: 6px; text-align: center;');

            const content = new St.BoxLayout({
                vertical: false,
                x_align: Clutter.ActorAlign.CENTER,
                y_align: Clutter.ActorAlign.CENTER, 
            });
            content.set_style('spacing: 6px;');
            if (iconName) {
                content.add_child(new St.Icon({
                    icon_name: iconName,
                    icon_size: 16,
                    y_align: Clutter.ActorAlign.CENTER,
                }));
            }
            if (labelText) {
                content.add_child(new St.Label({
                    text: labelText,
                    y_align: Clutter.ActorAlign.CENTER,
                }));
            }
            btn.set_child(content);
            btn.connect('clicked', callback);
            return btn;
        };

        const createHoldButton = (iconName, labelText, pressCallback, releaseCallback) => {
            const btn = new St.Button({
                style_class: 'button',
                x_expand: true,
                can_focus: true,
            });
            btn.set_style('padding: 6px; text-align: center;');

            const content = new St.BoxLayout({
                vertical: false,
                x_align: Clutter.ActorAlign.CENTER,
                y_align: Clutter.ActorAlign.CENTER, 
            });
            content.set_style('spacing: 6px;');
            if (iconName) {
                content.add_child(new St.Icon({
                    icon_name: iconName,
                    icon_size: 16,
                    y_align: Clutter.ActorAlign.CENTER,
                }));
            }
            if (labelText) {
                content.add_child(new St.Label({
                    text: labelText,
                    y_align: Clutter.ActorAlign.CENTER,
                }));
            }
            btn.set_child(content);
            
            btn.connect('button-press-event', () => {
                pressCallback();
                return Clutter.EVENT_PROPAGATE;
            });
            btn.connect('button-release-event', () => {
                releaseCallback();
                return Clutter.EVENT_PROPAGATE;
            });
            btn.connect('leave-event', () => {
                releaseCallback();
                return Clutter.EVENT_PROPAGATE;
            });

            return btn;
        };

        const row1 = new St.BoxLayout({ vertical: false, x_expand: true });
        row1.set_style('spacing: 8px; margin: 4px 12px;');
        row1.add_child(createHoldButton('go-up-symbolic', '上', 
            () => this._proxy.UpRemote(),
            () => this._proxy.StopRemote()
        ));
        row1.add_child(createHoldButton('go-down-symbolic', '下', 
            () => this._proxy.DownRemote(),
            () => this._proxy.StopRemote()
        ));
        row1.add_child(createButton('media-playback-stop-symbolic', '停', () => this._proxy.StopRemote()));

        // const row2 = new St.BoxLayout({ vertical: false, x_expand: true });
        // row1.set_style('spacing: 8px; margin: 4px 12px;');
        row1.add_child(createButton('', '75cm', () => this._proxy.MoveToSitRemote()));
        row1.add_child(createButton('', '110cm', () => this._proxy.MoveToStandRemote()));

        const mainBox = new St.BoxLayout({ vertical: true, x_expand: true });
        mainBox.add_child(row1);
        // mainBox.add_child(row2);

        const ctrlItem = new PopupMenu.PopupBaseMenuItem({ reactive: false, can_focus: false });
        ctrlItem.set_style('padding: 0px;'); 
        ctrlItem.add_child(mainBox);
        this.menu.addMenuItem(ctrlItem);

        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        const labelItem = new PopupMenu.PopupBaseMenuItem({ reactive: false, can_focus: false });
        this._label = new St.Label({
            text: '--- cm',
            style_class: 'desk-position-label',
        });
        labelItem.add_child(this._label);
        this.menu.addMenuItem(labelItem);

        this.menu.connect('open-state-changed', (menu, isOpen) => {
            if (isOpen) {
                this._proxy.RefreshPositionRemote();
            }
        });

        this._proxy.connect('g-properties-changed', (proxy, changed) => {
            const changedProps = changed.deep_unpack();
            if ('Position' in changedProps) {
                this._updatePosition(changedProps['Position'].unpack());
            }
        });
        
        // Initial sync
        this._updatePosition(this._proxy.Position);
    }

    _updatePosition(pos) {
        if (pos) {
            this.subtitle = `${pos.toFixed(1)} cm`;
            this._label.text = `位置: ${pos.toFixed(1)} cm`;
        }
    }
});

export default class LinakDeskExtension extends Extension {
    enable() {
        this._indicator = new QuickSettings.SystemIndicator();
        this._toggle = new DeskToggle(this);
        this._indicator.quickSettingsItems.push(this._toggle);

        Main.panel.statusArea.quickSettings.addExternalIndicator(this._indicator);
    }

    disable() {
        this._indicator.quickSettingsItems.forEach(item => item.destroy());
        this._indicator.destroy();
        this._indicator = null;
        this._toggle = null;
    }
}
