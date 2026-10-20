"""The scripted devices Betty's end-to-end tests talk to."""

from __future__ import annotations

from typing import Callable

from bumble import gatt, hci, hid, rfcomm, sdp
from bumble.core import (
    BT_BR_EDR_TRANSPORT,
    BT_HIDP_PROTOCOL_ID,
    BT_HUMAN_INTERFACE_DEVICE_SERVICE,
    BT_L2CAP_PROTOCOL_ID,
    UUID,
    AdvertisingData,
)
from bumble.device import Device
from bumble.pairing import PairingConfig, PairingDelegate
from bumble.profiles.battery_service import BatteryService
from bumble.profiles.device_information_service import DeviceInformationService

from . import sony

Emit = Callable[[dict], None]

# A custom GATT service for read, write, and notification tests.
TEST_SERVICE = "B0E7E2E0-0000-4000-8000-000000000001"
NOTIFY_CHARACTERISTIC = "B0E7E2E0-0000-4000-8000-000000000002"
WRITE_CHARACTERISTIC = "B0E7E2E0-0000-4000-8000-000000000003"

FIRMWARE_REVISION = "1.2.3"
_SDP_HANDLE = 0x00010001
# Class of device: audio, loudspeaker.
_SPEAKER_CLASS = 0x240414
# Class of device: audio, headphones.
_HEADPHONES_CLASS = 0x240418
# Class of device: peripheral, keyboard.
_KEYBOARD_CLASS = 0x002540

# A standard boot-protocol keyboard report descriptor.
_KEYBOARD_REPORT_DESCRIPTOR = bytes.fromhex(
    "05010906a101050719e029e71500250175019508810295017508810195057501"
    "050819012905910295017503910195067508150025650507190029658100c0"
)


class _Delegate(PairingDelegate):
    """Accepts or refuses pairing without user interaction (Just Works)."""

    def __init__(self, persona: Persona) -> None:
        super().__init__(io_capability=PairingDelegate.NO_OUTPUT_NO_INPUT)
        self.persona = persona

    async def accept(self) -> bool:
        return self.persona.accept_pairing

    async def confirm(self, auto: bool = False) -> bool:
        return self.persona.accept_pairing

    async def compare_numbers(self, number: int, digits: int) -> bool:
        return self.persona.accept_pairing


class Persona:
    """One scripted device. Subclasses set it up; commands are shared."""

    kind = ""

    def __init__(self, index: int, name: str, device: Device, emit: Emit) -> None:
        self.index = index
        self.name = name
        self.device = device
        self.emit = emit
        self.accept_pairing = True
        device.pairing_config_factory = lambda _connection: PairingConfig(
            sc=True, mitm=False, bonding=True, delegate=_Delegate(self)
        )
        device.on(Device.EVENT_CONNECTION, self._on_connection)

    def _event(self, name: str, **fields) -> None:
        self.emit({"event": name, "device": self.index, **fields})

    def _on_connection(self, connection) -> None:
        transport = "classic" if connection.transport == BT_BR_EDR_TRANSPORT else "le"
        self._event("connected", peer=str(connection.peer_address), transport=transport)
        connection.on(connection.EVENT_DISCONNECTION, lambda reason: self._event("disconnected", reason=reason))
        connection.on(connection.EVENT_PAIRING, lambda *_: self._event("paired"))
        connection.on(connection.EVENT_PAIRING_FAILURE, lambda reason: self._event("pairing_failed", reason=reason))

    @property
    def address(self) -> str:
        """The address Betty sees: random static for BLE, public for Classic."""
        address = self.device.random_address if self.kind == "sensor" else self.device.public_address
        return str(address).split("/")[0]

    async def start(self) -> None:
        raise NotImplementedError

    async def drop(self) -> None:
        for connection in list(self.device.connections.values()):
            await connection.disconnect()

    async def connect_host(self, host_address: str) -> None:
        await self.device.connect(host_address, transport=BT_BR_EDR_TRANSPORT)

    async def set_battery(self, level: int) -> None:
        raise ValueError(f"{self.kind} has no battery")

    async def notify(self, value: bytes) -> None:
        raise ValueError(f"{self.kind} has no notifying characteristic")


class Sensor(Persona):
    """A BLE peripheral with Battery, Device Information, and a test service."""

    kind = "sensor"

    async def start(self) -> None:
        self.battery = 87
        self.battery_service = BatteryService(lambda _connection: self.battery)
        information = DeviceInformationService(
            manufacturer_name="Betty",
            model_number="E2E Sensor",
            serial_number=f"E2E-{self.index:04d}",
            firmware_revision=FIRMWARE_REVISION,
        )
        self.notifier = gatt.Characteristic(
            NOTIFY_CHARACTERISTIC,
            gatt.Characteristic.Properties.READ | gatt.Characteristic.Properties.NOTIFY,
            gatt.Characteristic.READABLE,
            bytes([0]),
        )
        writable = gatt.Characteristic(
            WRITE_CHARACTERISTIC,
            gatt.Characteristic.Properties.WRITE | gatt.Characteristic.Properties.WRITE_WITHOUT_RESPONSE,
            gatt.Characteristic.WRITEABLE,
            gatt.CharacteristicValue(write=self._on_write),
        )
        self.device.add_services(
            [self.battery_service, information, gatt.Service(TEST_SERVICE, [self.notifier, writable])]
        )
        await self.device.power_on()
        advertising = AdvertisingData(
            [
                # Without discoverable flags, BlueZ ignores the advertiser
                # during discovery, as it would a real device.
                (
                    AdvertisingData.FLAGS,
                    bytes([AdvertisingData.LE_GENERAL_DISCOVERABLE_MODE_FLAG | AdvertisingData.BR_EDR_NOT_SUPPORTED_FLAG]),
                ),
                (AdvertisingData.COMPLETE_LOCAL_NAME, self.name.encode()),
                (AdvertisingData.COMPLETE_LIST_OF_16_BIT_SERVICE_CLASS_UUIDS, bytes(UUID("180F"))),
            ]
        )
        # Advertise from the random static address, as most BLE peripherals do.
        # Bumble's virtual link also routes LE data by random address only.
        await self.device.start_advertising(
            own_address_type=hci.OwnAddressType.RANDOM,
            auto_restart=True,
            advertising_data=bytes(advertising),
        )

    def _on_write(self, _connection, value: bytes) -> None:
        self._event("written", value=value.hex())

    async def set_battery(self, level: int) -> None:
        self.battery = level
        await self.device.notify_subscribers(self.battery_service.battery_level_characteristic)

    async def notify(self, value: bytes) -> None:
        self.notifier.value = value
        await self.device.notify_subscribers(self.notifier, value)


class _Classic(Persona):
    class_of_device = 0
    eir_service: str | None = None

    async def start(self) -> None:
        self.device.classic_enabled = True
        self.device.le_enabled = False
        self.device.class_of_device = self.class_of_device
        self.setup_classic()
        await self.device.power_on()
        await self._write_eir()
        await self.device.set_discoverable(True)
        await self.device.set_connectable(True)

    def setup_classic(self) -> None:
        pass

    async def _write_eir(self) -> None:
        structures = [(AdvertisingData.COMPLETE_LOCAL_NAME, self.name.encode())]
        if self.eir_service:
            structures.append(
                (AdvertisingData.COMPLETE_LIST_OF_128_BIT_SERVICE_CLASS_UUIDS, bytes(UUID(self.eir_service)))
            )
        await self.device.host.send_command(
            hci.HCI_Write_Extended_Inquiry_Response_Command(
                fec_required=0,
                extended_inquiry_response=bytes(AdvertisingData(structures)).ljust(240, b"\x00"),
            )
        )


class Speaker(_Classic):
    """A Classic device that pairs with Just Works and accepts connections."""

    kind = "speaker"
    class_of_device = _SPEAKER_CLASS


class SonyHeadset(_Classic):
    """A Classic device serving Sony's v2 control protocol over RFCOMM."""

    kind = "sony"
    class_of_device = _HEADPHONES_CLASS
    eir_service = sony.SERVICE_V2

    def setup_classic(self) -> None:
        self.headset = sony.Headset(battery=73)
        server = rfcomm.Server(self.device)
        channel = server.listen(self._on_dlc)
        self.device.sdp_service_records = {
            _SDP_HANDLE: rfcomm.make_service_sdp_records(_SDP_HANDLE, channel, UUID(sony.SERVICE_V2))
        }

    def _on_dlc(self, dlc) -> None:
        self._event("rfcomm_opened")

        def on_data(data: bytes) -> None:
            for reply in self.headset.receive(data):
                dlc.write(reply)

        dlc.sink = on_data

    async def set_battery(self, level: int) -> None:
        self.headset.battery = level


class Keyboard(_Classic):
    """A Classic HID keyboard, which BlueZ connects with its own input plugin.

    Unlike the speaker, it has a profile BlueZ connects without other daemons,
    so it exercises bt connect and disconnect.
    """

    kind = "keyboard"
    class_of_device = _KEYBOARD_CLASS
    eir_service = None

    def setup_classic(self) -> None:
        self.hid = hid.Device(self.device)
        self.hid.on(self.hid.EVENT_INTERRUPT_CHANNEL_OPEN, lambda: self._event("hid_connected"))
        self.device.sdp_service_records = {_SDP_HANDLE: _hid_sdp_records(_SDP_HANDLE, self.name)}


def _hid_sdp_records(handle: int, name: str) -> list[sdp.ServiceAttribute]:
    element = sdp.DataElement
    attribute = sdp.ServiceAttribute
    l2cap = lambda psm: element.sequence([element.uuid(BT_L2CAP_PROTOCOL_ID), element.unsigned_integer_16(psm)])
    hidp = element.sequence([element.uuid(BT_HIDP_PROTOCOL_ID)])
    return [
        attribute(sdp.SDP_SERVICE_RECORD_HANDLE_ATTRIBUTE_ID, element.unsigned_integer_32(handle)),
        attribute(sdp.SDP_SERVICE_CLASS_ID_LIST_ATTRIBUTE_ID, element.sequence([element.uuid(BT_HUMAN_INTERFACE_DEVICE_SERVICE)])),
        attribute(sdp.SDP_PROTOCOL_DESCRIPTOR_LIST_ATTRIBUTE_ID, element.sequence([l2cap(hid.HID_CONTROL_PSM), hidp])),
        attribute(sdp.SDP_BROWSE_GROUP_LIST_ATTRIBUTE_ID, element.sequence([element.uuid(sdp.SDP_PUBLIC_BROWSE_ROOT)])),
        attribute(
            sdp.SDP_LANGUAGE_BASE_ATTRIBUTE_ID_LIST_ATTRIBUTE_ID,
            element.sequence([element.unsigned_integer_16(0x656E), element.unsigned_integer_16(0x006A), element.unsigned_integer_16(0x0100)]),
        ),
        attribute(
            sdp.SDP_BLUETOOTH_PROFILE_DESCRIPTOR_LIST_ATTRIBUTE_ID,
            element.sequence([element.sequence([element.uuid(BT_HUMAN_INTERFACE_DEVICE_SERVICE), element.unsigned_integer_16(0x0101)])]),
        ),
        attribute(
            sdp.SDP_ADDITIONAL_PROTOCOL_DESCRIPTOR_LIST_ATTRIBUTE_ID,
            element.sequence([element.sequence([l2cap(hid.HID_INTERRUPT_PSM), hidp])]),
        ),
        attribute(0x0100, element.text_string(name.encode())),  # service name
        attribute(0x0201, element.unsigned_integer_16(0x0111)),  # HID parser version
        attribute(0x0202, element.unsigned_integer_8(0x40)),  # device subclass: keyboard
        attribute(0x0203, element.unsigned_integer_8(0x00)),  # country code
        attribute(0x0204, element.boolean(True)),  # virtual cable
        attribute(0x0205, element.boolean(True)),  # reconnect initiate
        attribute(
            0x0206,  # descriptor list: one report descriptor
            element.sequence([element.sequence([element.unsigned_integer_8(0x22), element.text_string(_KEYBOARD_REPORT_DESCRIPTOR)])]),
        ),
        attribute(
            0x0207,  # language ID base list
            element.sequence([element.sequence([element.unsigned_integer_16(0x0409), element.unsigned_integer_16(0x0100)])]),
        ),
        attribute(0x020B, element.unsigned_integer_16(0x0100)),  # profile version
        attribute(0x020C, element.unsigned_integer_16(0x0C80)),  # supervision timeout
        attribute(0x020D, element.boolean(True)),  # normally connectable
        attribute(0x020E, element.boolean(True)),  # boot device
    ]


KINDS: dict[str, type[Persona]] = {cls.kind: cls for cls in (Sensor, Speaker, SonyHeadset, Keyboard)}
