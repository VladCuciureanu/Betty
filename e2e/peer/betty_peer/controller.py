"""A dual-mode Bumble virtual controller that also answers Classic inquiry.

Bumble's software controller implements Classic connections and pairing, but
by default it reports itself as LE-only, and it does not implement the
Inquiry command, which BlueZ uses to discover Classic devices. This subclass
reports Classic support and answers inquiry with every other Classic
controller on the same virtual link that has inquiry scan enabled.
"""

import asyncio
import sys

from bumble import hci
from bumble.controller import Controller
from bumble.core import PhysicalTransport
from bumble.link import LocalLink

# Write Scan Enable values that include inquiry scan.
_INQUIRY_SCAN = 0x01
# How long a virtual inquiry takes; real inquiries take seconds, but nothing
# here needs that.
_INQUIRY_DELAY = 0.2


class ConnectionAddressLink(LocalLink):
    """A virtual link that tags LE data with the connection's own address.

    Bumble's link tags LE data with the sender's current random address. Linux
    changes that address while connected, for privacy, after which the peer
    no longer recognizes the data and drops it.
    """

    def send_acl_data(self, sender_controller, destination_address, transport, data):
        if transport != PhysicalTransport.LE:
            return super().send_acl_data(sender_controller, destination_address, transport, data)
        connection = sender_controller.le_connections.get(destination_address)
        receiver = self.find_le_controller(destination_address)
        if connection is None or receiver is None:
            return super().send_acl_data(sender_controller, destination_address, transport, data)
        source = connection.self_address
        asyncio.get_running_loop().call_soon(lambda: receiver.on_link_acl_data(source, transport, data))


_DUAL_MODE_FEATURES = (
    hci.LmpFeatureMask.LE_SUPPORTED_CONTROLLER
    | hci.LmpFeatureMask.EXTENDED_FEATURES
    | hci.LmpFeatureMask.ENCRYPTION
    | hci.LmpFeatureMask.RSSI_WITH_INQUIRY_RESULTS
    | hci.LmpFeatureMask.EXTENDED_INQUIRY_RESPONSE
    | hci.LmpFeatureMask.SIMULTANEOUS_LE_AND_BR_EDR_TO_SAME_DEVICE_CAPABLE_CONTROLLER
    | hci.LmpFeatureMask.SECURE_SIMPLE_PAIRING_CONTROLLER_SUPPORT
)

# Classic commands this controller handles, added to the supported-commands
# bitmap so that the host uses them.
_CLASSIC_COMMANDS = [
    "INQUIRY",
    "INQUIRY_CANCEL",
    "WRITE_INQUIRY_MODE",
    "CREATE_CONNECTION",
    "ACCEPT_CONNECTION_REQUEST",
    "REMOTE_NAME_REQUEST",
    "READ_REMOTE_SUPPORTED_FEATURES",
    "READ_REMOTE_EXTENDED_FEATURES",
    "AUTHENTICATION_REQUESTED",
    "IO_CAPABILITY_REQUEST_REPLY",
    "IO_CAPABILITY_REQUEST_NEGATIVE_REPLY",
    "USER_CONFIRMATION_REQUEST_REPLY",
    "USER_CONFIRMATION_REQUEST_NEGATIVE_REPLY",
    "USER_PASSKEY_REQUEST_REPLY",
    "USER_PASSKEY_REQUEST_NEGATIVE_REPLY",
    "LINK_KEY_REQUEST_REPLY",
    "LINK_KEY_REQUEST_NEGATIVE_REPLY",
    "WRITE_LOCAL_NAME",
    "READ_LOCAL_NAME",
    "WRITE_SCAN_ENABLE",
    "READ_CLASS_OF_DEVICE",
    "WRITE_CLASS_OF_DEVICE",
    "WRITE_EXTENDED_INQUIRY_RESPONSE",
    "WRITE_SIMPLE_PAIRING_MODE",
    "READ_SIMPLE_PAIRING_MODE",
    "READ_LE_HOST_SUPPORT",
    "WRITE_LE_HOST_SUPPORT",
    "READ_LOCAL_EXTENDED_FEATURES",
]


# Classic setup and housekeeping commands that Linux sends and Bumble's
# controller does not implement, answered as a basic real controller would.
# Values are return parameters; None means plain success.
_DEFAULT_REPLIES = {
    "HCI_READ_VOICE_SETTING_COMMAND": {"voice_setting": 0x0060},
    "HCI_READ_NUMBER_OF_SUPPORTED_IAC_COMMAND": {"num_support_iac": 1},
    # The General Inquiry Access Code, little-endian.
    "HCI_READ_CURRENT_IAC_LAP_COMMAND": {"num_support_iac": 1, "iac_lap": bytes([0x33, 0x8B, 0x9E])},
    "HCI_READ_PAGE_SCAN_ACTIVITY_COMMAND": {"page_scan_interval": 0x0800, "page_scan_window": 0x0012},
    "HCI_READ_PAGE_SCAN_TYPE_COMMAND": {"page_scan_type": 0},
    "HCI_READ_STORED_LINK_KEY_COMMAND": {"max_num_keys": 0, "num_keys_read": 0},
    "HCI_DELETE_STORED_LINK_KEY_COMMAND": {"num_keys_deleted": 0},
    "HCI_READ_AUTHENTICATION_ENABLE_COMMAND": {"authentication_enable": 0},
    "HCI_SET_EVENT_FILTER_COMMAND": None,
    "HCI_WRITE_CONNECTION_ACCEPT_TIMEOUT_COMMAND": None,
    "HCI_WRITE_PAGE_TIMEOUT_COMMAND": None,
    "HCI_WRITE_DEFAULT_LINK_POLICY_SETTINGS_COMMAND": None,
    "HCI_WRITE_PAGE_SCAN_ACTIVITY_COMMAND": None,
    "HCI_WRITE_INQUIRY_SCAN_ACTIVITY_COMMAND": None,
    "HCI_WRITE_LINK_POLICY_SETTINGS_COMMAND": None,
}


class InquiryController(Controller):
    class_of_device: int = 0
    extended_inquiry_response: bytes = b""

    def __init__(self, *args, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self.lmp_features = _DUAL_MODE_FEATURES
        self.supported_commands = set(self.supported_commands) | {
            opcode
            for name in _CLASSIC_COMMANDS
            if (opcode := getattr(hci, f"HCI_{name}_COMMAND", None)) is not None
        }

    def on_hci_command(self, command):
        if command.name in _DEFAULT_REPLIES:
            fields = _DEFAULT_REPLIES[command.name]
            if fields is None:
                return hci.HCI_StatusReturnParameters(hci.HCI_ErrorCode.SUCCESS)
            replies = getattr(hci, type(command).__name__.replace("_Command", "_ReturnParameters"))
            return replies(status=hci.HCI_ErrorCode.SUCCESS, **fields)
        # Name the controller: the kernel and the scripted devices' hosts
        # talk to different controllers on the same link.
        print(f"betty_peer: controller {self.name} does not implement {command.name}", file=sys.stderr, flush=True)
        return super().on_hci_command(command)

    def on_hci_authentication_requested_command(self, command):
        # A real controller first asks the host for a stored link key, so a
        # paired device re-authenticates without pairing again; Bumble's
        # controller goes straight to pairing. Its link key replies already
        # complete authentication, and a negative reply starts pairing.
        connection = self.find_classic_connection_by_handle(command.connection_handle)
        if connection is None:
            return super().on_hci_authentication_requested_command(command)
        self._send_hci_command_status(hci.HCI_COMMAND_STATUS_PENDING, command.op_code)
        self.send_hci_packet(hci.HCI_Link_Key_Request_Event(bd_addr=connection.peer_address))
        return None

    def on_hci_set_connection_encryption_command(self, command):
        # Bumble implements only LE encryption. Report Classic encryption as
        # on, which is all the host checks; the link itself is virtual.
        self._send_hci_command_status(hci.HCI_ErrorCode.SUCCESS, command.op_code)
        self.send_hci_packet(
            hci.HCI_Encryption_Change_Event(
                status=hci.HCI_ErrorCode.SUCCESS,
                connection_handle=command.connection_handle,
                encryption_enabled=command.encryption_enable,
            )
        )

    def on_hci_write_class_of_device_command(self, command):
        self.class_of_device = command.class_of_device
        return hci.HCI_StatusReturnParameters(hci.HCI_ErrorCode.SUCCESS)

    def on_hci_write_inquiry_mode_command(self, command):
        # Results always use the extended format, which every mode accepts.
        self.inquiry_mode = command.inquiry_mode
        return hci.HCI_StatusReturnParameters(hci.HCI_ErrorCode.SUCCESS)

    def on_hci_write_extended_inquiry_response_command(self, command):
        self.extended_inquiry_response = bytes(command.extended_inquiry_response)
        return hci.HCI_StatusReturnParameters(hci.HCI_ErrorCode.SUCCESS)

    def on_hci_inquiry_command(self, command):
        self._send_hci_command_status(hci.HCI_ErrorCode.SUCCESS, command.op_code)
        asyncio.get_running_loop().call_later(_INQUIRY_DELAY, self._report_inquiry)

    def on_hci_inquiry_cancel_command(self, _command):
        return hci.HCI_StatusReturnParameters(hci.HCI_ErrorCode.SUCCESS)

    def _report_inquiry(self):
        for other in list(self.link.controllers):
            if other is self or not (other.classic_scan_enable & _INQUIRY_SCAN):
                continue
            response = getattr(other, "extended_inquiry_response", b"") or _name_eir(other.local_name)
            self.send_hci_packet(
                hci.HCI_Extended_Inquiry_Result_Event(
                    num_responses=1,
                    bd_addr=other.public_address,
                    page_scan_repetition_mode=1,
                    reserved=0,
                    class_of_device=getattr(other, "class_of_device", 0),
                    clock_offset=0,
                    rssi=-40,
                    extended_inquiry_response=response.ljust(240, b"\x00")[:240],
                )
            )
        self.send_hci_packet(hci.HCI_Inquiry_Complete_Event(status=hci.HCI_ErrorCode.SUCCESS))


def _name_eir(name: str) -> bytes:
    encoded = name.encode("utf-8")[:238]
    # EIR structure: length, type 0x09 (Complete Local Name), data.
    return bytes([len(encoded) + 1, 0x09]) + encoded
