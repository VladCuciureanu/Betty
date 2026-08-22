#include "BettyAAP.h"

static NTSTATUS BettySubmitBrb(WDFDEVICE Device, PBRB Brb)
{
    WDFIOTARGET target = WdfDeviceGetIoTarget(Device);
    WDFREQUEST request;
    PIRP irp;
    PIO_STACK_LOCATION stack;
    WDF_REQUEST_SEND_OPTIONS options;
    NTSTATUS status;

    status = WdfRequestCreate(WDF_NO_OBJECT_ATTRIBUTES, target, &request);
    if (!NT_SUCCESS(status)) return status;
    irp = WdfRequestWdmGetIrp(request);
    stack = IoGetNextIrpStackLocation(irp);
    stack->MajorFunction = IRP_MJ_INTERNAL_DEVICE_CONTROL;
    stack->MinorFunction = 0;
    stack->Parameters.DeviceIoControl.IoControlCode = IOCTL_INTERNAL_BTH_SUBMIT_BRB;
    stack->Parameters.Others.Argument1 = Brb;
    WDF_REQUEST_SEND_OPTIONS_INIT(&options, WDF_REQUEST_SEND_OPTION_SYNCHRONOUS | WDF_REQUEST_SEND_OPTION_TIMEOUT);
    WDF_REQUEST_SEND_OPTIONS_SET_TIMEOUT(&options, WDF_REL_TIMEOUT_IN_SEC(10));
    (void)WdfRequestSend(request, target, &options);
    status = WdfRequestGetStatus(request);
    WdfObjectDelete(request);
    return status;
}

static VOID BettyL2capIndication(PVOID CallbackContext, INDICATION_CODE Indication, PINDICATION_PARAMETERS Parameters)
{
    WDFDEVICE device = (WDFDEVICE)CallbackContext;
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(device);
    UNREFERENCED_PARAMETER(Parameters);
    if (Indication == IndicationRemoteDisconnect) {
        (void)InterlockedExchangePointer((PVOID volatile *)&context->Channel, NULL);
        context->Address = 0;
    }
}

NTSTATUS BettyOpenChannel(WDFDEVICE Device, BTH_ADDR Address, USHORT Psm)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    struct _BRB_L2CA_OPEN_CHANNEL *open;
    NTSTATUS status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (!context->InterfaceReady) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_NOT_READY;
    }
    if (context->Channel != NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_BUSY;
    }
    open = (struct _BRB_L2CA_OPEN_CHANNEL *)context->Profile.BthAllocateBrb(BRB_L2CA_OPEN_CHANNEL, BETTY_POOL_TAG);
    if (open == NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    open->BtAddress = Address;
    open->Psm = Psm;
    open->ChannelFlags = CF_ROLE_EITHER;
    open->ConfigOut.Flags = 0;
    open->ConfigIn.Flags = 0;
    open->IncomingQueueDepth = 10;
    open->CallbackFlags = CALLBACK_DISCONNECT;
    open->Callback = BettyL2capIndication;
    open->CallbackContext = Device;
    open->ReferenceObject = WdfDeviceWdmGetDeviceObject(Device);
    status = BettySubmitBrb(Device, (PBRB)open);
    if (NT_SUCCESS(status)) {
        context->Channel = open->ChannelHandle;
        context->Address = Address;
    }
    context->Profile.BthFreeBrb((PBRB)open);
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS BettyCloseChannel(WDFDEVICE Device)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    struct _BRB_L2CA_CLOSE_CHANNEL *close;
    NTSTATUS status = STATUS_SUCCESS;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (context->Channel == NULL || !context->InterfaceReady) {
        context->Channel = NULL;
        context->Address = 0;
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_SUCCESS;
    }
    close = (struct _BRB_L2CA_CLOSE_CHANNEL *)context->Profile.BthAllocateBrb(BRB_L2CA_CLOSE_CHANNEL, BETTY_POOL_TAG);
    if (close == NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    close->ChannelHandle = context->Channel;
    close->BtAddress = context->Address;
    status = BettySubmitBrb(Device, (PBRB)close);
    context->Profile.BthFreeBrb((PBRB)close);
    context->Channel = NULL;
    context->Address = 0;
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS BettyTransfer(WDFDEVICE Device, BOOLEAN Incoming, PVOID Buffer, ULONG *Size, ULONG TimeoutMs)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    struct _BRB_L2CA_ACL_TRANSFER *transfer;
    NTSTATUS status;

    WdfWaitLockAcquire(context->ChannelLock, NULL);
    if (context->Channel == NULL || !context->InterfaceReady) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_DEVICE_NOT_CONNECTED;
    }
    transfer = (struct _BRB_L2CA_ACL_TRANSFER *)context->Profile.BthAllocateBrb(BRB_L2CA_ACL_TRANSFER, BETTY_POOL_TAG);
    if (transfer == NULL) {
        WdfWaitLockRelease(context->ChannelLock);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    transfer->BtAddress = context->Address;
    transfer->ChannelHandle = context->Channel;
    transfer->TransferFlags = Incoming ? (ACL_TRANSFER_DIRECTION_IN | ACL_SHORT_TRANSFER_OK) : ACL_TRANSFER_DIRECTION_OUT;
    if (Incoming && TimeoutMs != 0) {
        transfer->TransferFlags |= ACL_TRANSFER_TIMEOUT;
        transfer->Timeout = TimeoutMs;
    }
    transfer->BufferSize = *Size;
    transfer->Buffer = Buffer;
    transfer->BufferMDL = NULL;
    status = BettySubmitBrb(Device, (PBRB)transfer);
    if (NT_SUCCESS(status)) {
        *Size = transfer->BufferSize;
    }
    context->Profile.BthFreeBrb((PBRB)transfer);
    WdfWaitLockRelease(context->ChannelLock);
    return status;
}

NTSTATUS DriverEntry(PDRIVER_OBJECT DriverObject, PUNICODE_STRING RegistryPath)
{
    WDF_DRIVER_CONFIG config;
    WDF_DRIVER_CONFIG_INIT(&config, BettyEvtDeviceAdd);
    return WdfDriverCreate(DriverObject, RegistryPath, WDF_NO_OBJECT_ATTRIBUTES, &config, WDF_NO_HANDLE);
}

NTSTATUS BettyEvtDeviceAdd(WDFDRIVER Driver, PWDFDEVICE_INIT DeviceInit)
{
    WDF_OBJECT_ATTRIBUTES attributes;
    WDF_PNPPOWER_EVENT_CALLBACKS pnp;
    WDF_FILEOBJECT_CONFIG files;
    WDF_IO_QUEUE_CONFIG queue;
    WDFDEVICE device;
    PBETTY_DEVICE_CONTEXT context;
    NTSTATUS status;
    UNREFERENCED_PARAMETER(Driver);

    WDF_PNPPOWER_EVENT_CALLBACKS_INIT(&pnp);
    pnp.EvtDevicePrepareHardware = BettyEvtPrepareHardware;
    pnp.EvtDeviceReleaseHardware = BettyEvtReleaseHardware;
    WdfDeviceInitSetPnpPowerEventCallbacks(DeviceInit, &pnp);
    WDF_FILEOBJECT_CONFIG_INIT(&files, WDF_NO_EVENT_CALLBACK, BettyEvtFileClose, WDF_NO_EVENT_CALLBACK);
    files.AutoForwardCleanupClose = WdfFalse;
    WdfDeviceInitSetFileObjectConfig(DeviceInit, &files, WDF_NO_OBJECT_ATTRIBUTES);
    WdfDeviceInitSetDeviceType(DeviceInit, FILE_DEVICE_BLUETOOTH);
    WdfDeviceInitSetExclusive(DeviceInit, TRUE);
    WdfDeviceInitSetIoType(DeviceInit, WdfDeviceIoBuffered);
    WDF_OBJECT_ATTRIBUTES_INIT_CONTEXT_TYPE(&attributes, BETTY_DEVICE_CONTEXT);
    status = WdfDeviceCreate(&DeviceInit, &attributes, &device);
    if (!NT_SUCCESS(status)) return status;
    context = BettyGetDeviceContext(device);
    RtlZeroMemory(context, sizeof(*context));
    status = WdfWaitLockCreate(WDF_NO_OBJECT_ATTRIBUTES, &context->ChannelLock);
    if (!NT_SUCCESS(status)) return status;
    status = WdfDeviceCreateDeviceInterface(device, &GUID_DEVINTERFACE_BETTY_AAP, NULL);
    if (!NT_SUCCESS(status)) return status;
    WDF_IO_QUEUE_CONFIG_INIT_DEFAULT_QUEUE(&queue, WdfIoQueueDispatchSequential);
    queue.EvtIoDeviceControl = BettyEvtIoDeviceControl;
    return WdfIoQueueCreate(device, &queue, WDF_NO_OBJECT_ATTRIBUTES, WDF_NO_HANDLE);
}

NTSTATUS BettyEvtPrepareHardware(WDFDEVICE Device, WDFCMRESLIST Raw, WDFCMRESLIST Translated)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    NTSTATUS status;
    UNREFERENCED_PARAMETER(Raw);
    UNREFERENCED_PARAMETER(Translated);
    RtlZeroMemory(&context->Profile, sizeof(context->Profile));
    context->Profile.Interface.Size = sizeof(BTH_PROFILE_DRIVER_INTERFACE);
    context->Profile.Interface.Version = BTHDDI_PROFILE_DRIVER_INTERFACE_VERSION_FOR_QI;
    status = WdfFdoQueryForInterface(Device, &GUID_BTHDDI_PROFILE_DRIVER_INTERFACE,
        (PINTERFACE)&context->Profile, sizeof(context->Profile),
        BTHDDI_PROFILE_DRIVER_INTERFACE_VERSION_FOR_QI, NULL);
    context->InterfaceReady = NT_SUCCESS(status);
    return status;
}

VOID BettyEvtFileClose(WDFFILEOBJECT FileObject)
{
    (void)BettyCloseChannel(WdfFileObjectGetDevice(FileObject));
}

NTSTATUS BettyEvtReleaseHardware(WDFDEVICE Device, WDFCMRESLIST Translated)
{
    PBETTY_DEVICE_CONTEXT context = BettyGetDeviceContext(Device);
    UNREFERENCED_PARAMETER(Translated);
    (void)BettyCloseChannel(Device);
    if (context->InterfaceReady && context->Profile.InterfaceDereference != NULL) {
        context->Profile.InterfaceDereference(context->Profile.Context);
    }
    context->InterfaceReady = FALSE;
    return STATUS_SUCCESS;
}

VOID BettyEvtIoDeviceControl(WDFQUEUE Queue, WDFREQUEST Request, size_t OutputLength, size_t InputLength, ULONG IoControlCode)
{
    WDFDEVICE device = WdfIoQueueGetDevice(Queue);
    NTSTATUS status = STATUS_INVALID_DEVICE_REQUEST;
    size_t information = 0;
    PVOID input = NULL, output = NULL;
    size_t size;
    ULONG transferSize;
    UNREFERENCED_PARAMETER(InputLength);
    UNREFERENCED_PARAMETER(OutputLength);

    switch (IoControlCode) {
    case IOCTL_BETTY_CONNECT: {
        PBETTY_CONNECT_INPUT in;
        PBETTY_CONNECT_OUTPUT out;
        status = WdfRequestRetrieveInputBuffer(Request, sizeof(*in), (PVOID *)&in, &size);
        if (!NT_SUCCESS(status)) break;
        if (in->Psm != BETTY_AAP_PSM || in->BluetoothAddress == 0 || in->BluetoothAddress > 0xffffffffffffULL) {
            status = STATUS_INVALID_PARAMETER;
            break;
        }
        status = WdfRequestRetrieveOutputBuffer(Request, sizeof(*out), (PVOID *)&out, &size);
        if (!NT_SUCCESS(status)) break;
        status = BettyOpenChannel(device, in->BluetoothAddress, in->Psm);
        out->Success = NT_SUCCESS(status) ? 1u : 0u;
        out->Status = status;
        information = sizeof(*out);
        status = STATUS_SUCCESS;
        break;
    }
    case IOCTL_BETTY_DISCONNECT:
        status = BettyCloseChannel(device);
        break;
    case IOCTL_BETTY_SEND:
        status = WdfRequestRetrieveInputBuffer(Request, 1, &input, &size);
        if (!NT_SUCCESS(status)) break;
        if (size > BETTY_MAX_TRANSFER) { status = STATUS_INVALID_BUFFER_SIZE; break; }
        transferSize = (ULONG)size;
        status = BettyTransfer(device, FALSE, input, &transferSize, 0);
        break;
    case IOCTL_BETTY_RECEIVE: {
        ULONG timeout = 0;
        if (NT_SUCCESS(WdfRequestRetrieveInputBuffer(Request, sizeof(BETTY_RECEIVE_INPUT), &input, &size))) {
            timeout = ((PBETTY_RECEIVE_INPUT)input)->TimeoutMs;
        }
        status = WdfRequestRetrieveOutputBuffer(Request, 1, &output, &size);
        if (!NT_SUCCESS(status)) break;
        if (size > BETTY_MAX_TRANSFER) { status = STATUS_INVALID_BUFFER_SIZE; break; }
        transferSize = (ULONG)size;
        status = BettyTransfer(device, TRUE, output, &transferSize, timeout);
        if (NT_SUCCESS(status)) information = transferSize;
        break;
    }
    }
    WdfRequestCompleteWithInformation(Request, status, information);
}
