"""A dual-mode Bumble virtual controller that also answers Classic inquiry.

Bumble's software controller implements Classic connections and pairing, but
by default it reports itself as LE-only, and it does not implement the
Inquiry command, which BlueZ uses to discover Classic devices. This subclass
reports Classic support and answers inquiry with every other Classic
controller on the same virtual link that has inquiry scan enabled.
"""

import asyncio

from bumble import hci
from bumble.controller import Controller

# Write Scan Enable values that include inquiry scan.
_INQUIRY_SCAN = 0x01
# How long a virtual inquiry takes; real inquiries take seconds, but nothing
# here needs that.
_INQUIRY_DELAY = 0.2


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
