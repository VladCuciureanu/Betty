#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>
#import <IOKit/hid/IOHIDManager.h>
#import <IOKit/hid/IOHIDKeys.h>
#import <IOKit/hid/IOHIDUsageTables.h>
#include <stdlib.h>

static NSString *BettyStringProperty(IOHIDDeviceRef device, CFStringRef key) {
    CFTypeRef value = IOHIDDeviceGetProperty(device, key);
    return value && CFGetTypeID(value) == CFStringGetTypeID() ? (__bridge NSString *)value : nil;
}

static NSNumber *BettyNumberProperty(IOHIDDeviceRef device, CFStringRef key) {
    CFTypeRef value = IOHIDDeviceGetProperty(device, key);
    return value && CFGetTypeID(value) == CFNumberGetTypeID() ? (__bridge NSNumber *)value : nil;
}

static NSString *BettyAddress(NSString *address) {
    if (!address) return nil;
    NSString *normalized = [[address stringByReplacingOccurrencesOfString:@":" withString:@"-"] lowercaseString];
    NSRegularExpression *pattern = [NSRegularExpression regularExpressionWithPattern:@"^[0-9a-f]{2}(-[0-9a-f]{2}){5}$" options:0 error:nil];
    return [pattern firstMatchInString:normalized options:0 range:NSMakeRange(0, normalized.length)] ? normalized : nil;
}

static NSNumber *BettyHIDBattery(IOHIDDeviceRef device) {
    NSDictionary *matching = @{
        @kIOHIDElementUsagePageKey: @(kHIDPage_GenericDeviceControls),
        @kIOHIDElementUsageKey: @(kHIDUsage_GenDevControls_BatteryStrength),
    };
    CFArrayRef elements = IOHIDDeviceCopyMatchingElements(device, (__bridge CFDictionaryRef)matching, kIOHIDOptionsTypeNone);
    if (!elements) return nil;
    NSNumber *percentage = nil;
    for (id item in (__bridge NSArray *)elements) {
        IOHIDElementRef element = (__bridge IOHIDElementRef)item;
        IOHIDValueRef value = nil;
        if (IOHIDDeviceGetValue(device, element, &value) != kIOReturnSuccess || !value) continue;
        CFIndex minimum = IOHIDElementGetLogicalMin(element);
        CFIndex maximum = IOHIDElementGetLogicalMax(element);
        CFIndex current = IOHIDValueGetIntegerValue(value);
        if (maximum <= minimum || current < minimum || current > maximum) continue;
        NSInteger scaled = (NSInteger)(((current - minimum) * 100) / (maximum - minimum));
        if (scaled >= 0 && scaled <= 100) {
            percentage = @(scaled);
            break;
        }
    }
    CFRelease(elements);
    return percentage;
}

static void BettyAddObservation(NSMutableDictionary<NSString *, NSDictionary *> *observations, NSString *address, NSNumber *percentage, NSString *source) {
    address = BettyAddress(address);
    if (!address || !percentage || percentage.integerValue < 0 || percentage.integerValue > 100) return;
    if (!observations[address]) {
        observations[address] = @{ @"address": address, @"percentage": percentage, @"source": source };
    }
}

static void BettyCollectPublicHID(NSMutableDictionary<NSString *, NSDictionary *> *observations) {
    IOHIDManagerRef manager = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
    if (!manager) return;
    IOHIDManagerSetDeviceMatching(manager, NULL);
    if (IOHIDManagerOpen(manager, kIOHIDOptionsTypeNone) == kIOReturnSuccess) {
        CFSetRef devices = IOHIDManagerCopyDevices(manager);
        for (id item in (__bridge NSSet *)devices) {
            IOHIDDeviceRef device = (__bridge IOHIDDeviceRef)item;
            NSString *transport = BettyStringProperty(device, CFSTR(kIOHIDTransportKey));
            if (![transport isEqualToString:@kIOHIDTransportBluetoothValue] && ![transport isEqualToString:@kIOHIDTransportBluetoothLowEnergyValue]) continue;
            NSString *address = BettyStringProperty(device, CFSTR("DeviceAddress"));
            if (!BettyAddress(address)) address = BettyStringProperty(device, CFSTR(kIOHIDSerialNumberKey));
            NSNumber *percentage = BettyHIDBattery(device);
            BettyAddObservation(observations, address, percentage, @"macos-iohid-element");
        }
        if (devices) CFRelease(devices);
        IOHIDManagerClose(manager, kIOHIDOptionsTypeNone);
    }
    CFRelease(manager);
}

static NSString *BettyRegistryString(io_service_t service, CFStringRef key) {
    CFTypeRef value = IORegistryEntryCreateCFProperty(service, key, kCFAllocatorDefault, 0);
    if (!value) return nil;
    NSString *result = CFGetTypeID(value) == CFStringGetTypeID() ? [(__bridge NSString *)value copy] : nil;
    CFRelease(value);
    return result;
}

static NSNumber *BettyRegistryNumber(io_service_t service, CFStringRef key) {
    CFTypeRef value = IORegistryEntryCreateCFProperty(service, key, kCFAllocatorDefault, 0);
    if (!value) return nil;
    NSNumber *result = CFGetTypeID(value) == CFNumberGetTypeID() ? [(__bridge NSNumber *)value copy] : nil;
    CFRelease(value);
    return result;
}

static void BettyCollectRegistryFallback(NSMutableDictionary<NSString *, NSDictionary *> *observations) {
    io_iterator_t iterator = IO_OBJECT_NULL;
    CFMutableDictionaryRef matching = IOServiceMatching("AppleDeviceManagementHIDEventService");
    if (!matching || IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator) != KERN_SUCCESS) return;
    io_service_t service;
    while ((service = IOIteratorNext(iterator))) {
        NSString *transport = BettyRegistryString(service, CFSTR("Transport"));
        if ([transport isEqualToString:@kIOHIDTransportBluetoothValue] || [transport isEqualToString:@kIOHIDTransportBluetoothLowEnergyValue]) {
            NSString *address = BettyRegistryString(service, CFSTR("DeviceAddress"));
            if (!BettyAddress(address)) address = BettyRegistryString(service, CFSTR("SerialNumber"));
            BettyAddObservation(observations, address, BettyRegistryNumber(service, CFSTR("BatteryPercent")), @"macos-iokit-registry");
        }
        IOObjectRelease(service);
    }
    IOObjectRelease(iterator);
}

char *betty_copy_hid_batteries_json(void) {
    @autoreleasepool {
        NSMutableDictionary<NSString *, NSDictionary *> *observations = [NSMutableDictionary dictionary];
        BettyCollectPublicHID(observations);
        BettyCollectRegistryFallback(observations);
        NSData *data = [NSJSONSerialization dataWithJSONObject:observations.allValues options:0 error:nil];
        if (!data) return NULL;
        NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
        return json ? strdup(json.UTF8String) : NULL;
    }
}
